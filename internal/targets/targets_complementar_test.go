package targets_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

func TestOpenHeadersRejectsAuthenticatedInvalidJSONComplementar(t *testing.T) {
	for _, tc := range []struct {
		name, plain string
		syntax      bool
	}{
		{"sintaxe", "{", true}, {"tipo", `{"Authorization":23}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box := caixa(t)
			id := uuid.Must(uuid.NewV7())
			enc, err := box.Seal([]byte(tc.plain), crypto.AAD("targets", "headers_enc", id))
			if err != nil {
				t.Fatal(err)
			}
			h, err := targets.OpenHeaders(box, id, enc)
			if err == nil || h != nil || !strings.HasPrefix(err.Error(), "ler headers do alvo: ") {
				t.Fatalf("JSON autenticado inválido aceito: headers=%v, erro=%v", h, err)
			}
			if tc.syntax {
				var cause *json.SyntaxError
				if !errors.As(err, &cause) {
					t.Fatalf("causa SyntaxError perdida: %v", err)
				}
			} else {
				var cause *json.UnmarshalTypeError
				if !errors.As(err, &cause) {
					t.Fatalf("causa UnmarshalTypeError perdida: %v", err)
				}
			}
		})
	}
}

func TestHeaderValidationComplementar(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		reason  string
	}{
		{"soma", func() map[string]string {
			h := map[string]string{}
			for i := 0; i < 9; i++ {
				h[fmt.Sprintf("X-%d", i)] = strings.Repeat("a", 1000)
			}
			return h
		}(), targets.ReasonTooLong},
		{"nome_vazio", map[string]string{"": "valor-publico"}, targets.ReasonInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := novoAmbiente(t)
			dono := a.usuario(t)
			in := entrada()
			in.Headers = tc.headers
			got, err := a.svc.Create(context.Background(), dono, in)
			var cause *targets.ValidationError
			if !errors.As(err, &cause) || got.ID != uuid.Nil {
				t.Fatalf("entrada inválida aceita: %+v, %v", got, err)
			}
			if cause.Error() != "dados do alvo inválidos" || len(cause.Fields) != 1 || cause.Fields[0].Field != "headers" || cause.Fields[0].Reason != tc.reason {
				t.Fatalf("diagnóstico inválido: %+v, %q", cause, cause.Error())
			}
			rows, err := a.svc.List(context.Background(), dono, nil, 0)
			if err != nil || len(rows) != 0 {
				t.Fatalf("validação inseriu alvo: %v, %v", rows, err)
			}
		})
	}
}

func TestHeaderTABRoundTripComplementar(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	in := entrada()
	value := "Bearer\tcredencial-sintetica"
	in.Headers = map[string]string{"Authorization": value}
	got, err := a.svc.Create(context.Background(), dono, in)
	if err != nil {
		t.Fatal(err)
	}
	var enc []byte
	if err := a.admin.QueryRow(context.Background(), `SELECT headers_enc FROM targets WHERE id=$1`, got.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	h, err := targets.OpenHeaders(caixa(t), got.ID, enc)
	if err != nil || h["Authorization"] != value {
		t.Fatalf("TAB não preservado: %v, %v", h, err)
	}
	if len(got.HeaderNames) != 1 || got.HeaderNames[0] != "Authorization" || strings.Contains(string(enc), value) {
		t.Fatalf("header não protegido: %+v", got)
	}
}

// Cada alteração pertence ao banco descartável desta fixture. A restauração
// registrada depois de New roda antes do fechamento dos pools e do snapshot.
func alterarDDLComplementar(t *testing.T, a ambiente, apply, undo string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.admin.Exec(ctx, apply); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		t.Helper()
		if restored {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := a.admin.Exec(ctx, undo); err != nil {
			t.Errorf("restaurar DDL próprio: %v", err)
			return
		}
		restored = true
	}
	t.Cleanup(restore)
	return restore
}

func exigirPGComplementar(t *testing.T, err error, code, prefix string) {
	t.Helper()
	var cause *pgconn.PgError
	if !errors.As(err, &cause) || cause.Code != code || !strings.HasPrefix(err.Error(), prefix) || errors.Is(err, targets.ErrNotFound) {
		t.Fatalf("causa/contexto PostgreSQL perdidos: %v", err)
	}
}

func TestStoredInvalidJSONRecoveryComplementar(t *testing.T) {
	for _, operation := range []string{"Get", "List", "Update"} {
		t.Run(operation, func(t *testing.T) {
			a := novoAmbiente(t)
			dono := a.usuario(t)
			id := a.alvo(t, dono)
			ctx := context.Background()
			var original []byte
			if err := a.admin.QueryRow(ctx, `SELECT headers_enc FROM targets WHERE id=$1`, id).Scan(&original); err != nil {
				t.Fatal(err)
			}
			invalid, err := caixa(t).Seal([]byte("{"), crypto.AAD("targets", "headers_enc", id))
			if err != nil {
				t.Fatal(err)
			}
			restored := false
			restore := func() {
				t.Helper()
				if restored {
					return
				}
				c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := a.admin.Exec(c, `UPDATE targets SET headers_enc=$2 WHERE id=$1`, id, original); err != nil {
					t.Errorf("restaurar envelope: %v", err)
					return
				}
				restored = true
			}
			t.Cleanup(restore)
			if _, err := a.admin.Exec(ctx, `UPDATE targets SET headers_enc=$2 WHERE id=$1`, id, invalid); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "Get":
				got, e := a.svc.Get(ctx, dono, id)
				err = e
				if got.ID != uuid.Nil {
					t.Fatalf("Get retornou sucesso: %+v", got)
				}
			case "List":
				got, e := a.svc.List(ctx, dono, nil, 0)
				err = e
				if len(got) != 0 {
					t.Fatalf("List retornou sucesso: %+v", got)
				}
			case "Update":
				got, e := a.svc.Update(ctx, dono, id, targets.Patch{Name: ptr("Novo nome")})
				err = e
				if got.ID != uuid.Nil {
					t.Fatalf("Update retornou sucesso: %+v", got)
				}
			}
			var cause *json.SyntaxError
			if !errors.As(err, &cause) || !strings.HasPrefix(err.Error(), "ler headers do alvo: ") || errors.Is(err, targets.ErrNotFound) {
				t.Fatalf("causa JSON perdida: %v", err)
			}
			restore()
			got, err := a.svc.Get(ctx, dono, id)
			if err != nil || got.Name != "API" || len(got.HeaderNames) != 1 {
				t.Fatalf("recuperação alterou dados: %+v, %v", got, err)
			}
			switch operation {
			case "List":
				rows, e := a.svc.List(ctx, dono, nil, 0)
				if e != nil || len(rows) != 1 || rows[0].ID != id {
					t.Fatalf("List não recuperou: %v, %v", rows, e)
				}
			case "Update":
				updated, e := a.svc.Update(ctx, dono, id, targets.Patch{Name: ptr("Novo nome")})
				if e != nil || updated.Name != "Novo nome" {
					t.Fatalf("Update não recuperou: %v, %v", updated, e)
				}
			}
		})
	}
}

func TestServiceSQLRecoveryComplementar(t *testing.T) {
	type scenario struct {
		name, apply, undo, code, prefix string
		run                             func(*testing.T, ambiente, uuid.UUID, uuid.UUID, bool)
	}
	scenarios := []scenario{
		{"Get", `ALTER TABLE targets RENAME TO targets_indisponivel_complementar`, `ALTER TABLE targets_indisponivel_complementar RENAME TO targets`, "42P01", "buscar alvo: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Get(context.Background(), u, id)
			if broken {
				exigirPGComplementar(t, err, "42P01", "buscar alvo: ")
				if got.ID != uuid.Nil {
					t.Fatal("Get devolveu alvo no erro")
				}
			} else if err != nil || got.ID != id {
				t.Fatalf("Get não recuperou: %+v, %v", got, err)
			}
		}},
		{"List", `ALTER TABLE targets RENAME TO targets_indisponivel_complementar`, `ALTER TABLE targets_indisponivel_complementar RENAME TO targets`, "42P01", "listar alvos: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.List(context.Background(), u, nil, 0)
			if broken {
				exigirPGComplementar(t, err, "42P01", "listar alvos: ")
				if len(got) != 0 {
					t.Fatal("List devolveu alvos no erro")
				}
			} else if err != nil || len(got) != 1 || got[0].ID != id {
				t.Fatalf("List não recuperou: %+v, %v", got, err)
			}
		}},
		{"Create", `ALTER TABLE targets RENAME TO targets_indisponivel_complementar`, `ALTER TABLE targets_indisponivel_complementar RENAME TO targets`, "42P01", "gravar alvo: contar alvos: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Create(context.Background(), u, entrada())
			if broken {
				exigirPGComplementar(t, err, "42P01", "gravar alvo: contar alvos: ")
				if got.ID != uuid.Nil {
					t.Fatal("Create devolveu alvo no erro")
				}
			} else if err != nil || got.ID == uuid.Nil || got.ID == id {
				t.Fatalf("Create não recuperou: %+v, %v", got, err)
			}
		}},
		{"Update_busca", `ALTER TABLE targets RENAME TO targets_indisponivel_complementar`, `ALTER TABLE targets_indisponivel_complementar RENAME TO targets`, "42P01", "buscar alvo: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Update(context.Background(), u, id, targets.Patch{Name: ptr("Novo nome")})
			if broken {
				exigirPGComplementar(t, err, "42P01", "buscar alvo: ")
				if got.ID != uuid.Nil {
					t.Fatal("Update devolveu alvo no erro")
				}
			} else if err != nil || got.Name != "Novo nome" {
				t.Fatalf("Update não recuperou: %+v, %v", got, err)
			}
		}},
		{"Update_saude", `ALTER TABLE target_health RENAME TO target_health_indisponivel_complementar`, `ALTER TABLE target_health_indisponivel_complementar RENAME TO target_health`, "42P01", "zerar histerese do alvo: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Update(context.Background(), u, id, targets.Patch{URL: ptr("https://api.exemplo.com/v2/health")})
			if broken {
				exigirPGComplementar(t, err, "42P01", "zerar histerese do alvo: ")
				if got.ID != uuid.Nil {
					t.Fatal("Update devolveu alvo no erro")
				}
			} else if err != nil || got.URL != "https://api.exemplo.com/v2/health" {
				t.Fatalf("Update não recuperou: %+v, %v", got, err)
			}
		}},
		{"Update_gravacao", `ALTER TABLE targets ADD CONSTRAINT targets_nome_complementar CHECK (name <> 'Novo nome')`, `ALTER TABLE targets DROP CONSTRAINT targets_nome_complementar`, "23514", "atualizar alvo: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Update(context.Background(), u, id, targets.Patch{Name: ptr("Novo nome")})
			if broken {
				exigirPGComplementar(t, err, "23514", "atualizar alvo: ")
				if got.ID != uuid.Nil {
					t.Fatal("Update devolveu alvo no erro")
				}
			} else if err != nil || got.Name != "Novo nome" {
				t.Fatalf("Update não recuperou: %+v, %v", got, err)
			}
		}},
		{"Delete", `ALTER TABLE targets RENAME TO targets_indisponivel_complementar`, `ALTER TABLE targets_indisponivel_complementar RENAME TO targets`, "42P01", "apagar alvo: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			err := a.svc.Delete(context.Background(), u, id)
			if broken {
				exigirPGComplementar(t, err, "42P01", "apagar alvo: ")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.svc.Get(context.Background(), u, id); !errors.Is(err, targets.ErrNotFound) {
					t.Fatalf("Delete não persistiu: %v", err)
				}
			}
		}},
		{"Summary_alvos", `ALTER TABLE targets RENAME TO targets_indisponivel_complementar`, `ALTER TABLE targets_indisponivel_complementar RENAME TO targets`, "42P01", "listar alvos do resumo: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Summary(context.Background(), u)
			if broken {
				exigirPGComplementar(t, err, "42P01", "listar alvos do resumo: ")
				if len(got) != 0 {
					t.Fatal("Summary devolveu sucesso")
				}
			} else if err != nil || len(got) != 1 || got[0].ID != id {
				t.Fatalf("Summary não recuperou: %+v, %v", got, err)
			}
		}},
		{"Summary_marca", `ALTER TABLE check_rollup_1h RENAME TO rollup_indisponivel_complementar`, `ALTER TABLE rollup_indisponivel_complementar RENAME TO check_rollup_1h`, "42P01", "marca d'água dos rollups: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Summary(context.Background(), u)
			if broken {
				exigirPGComplementar(t, err, "42P01", "marca d'água dos rollups: ")
			} else if err != nil || len(got) != 1 || got[0].ID != id {
				t.Fatalf("Summary não recuperou: %+v, %v", got, err)
			}
		}},
		{"Checks_brutos", `ALTER TABLE checks RENAME TO checks_indisponivel_complementar`, `ALTER TABLE checks_indisponivel_complementar RENAME TO checks`, "42P01", "listar checks: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Checks(context.Background(), u, id, a.clk.Now().Add(-time.Hour), a.clk.Now())
			if broken {
				exigirPGComplementar(t, err, "42P01", "listar checks: ")
			} else if err != nil || len(got.Checks) != 1 || got.Checks[0].LatencyMS != 77 {
				t.Fatalf("Checks não recuperou: %+v, %v", got, err)
			}
		}},
		{"Checks_rollups", `ALTER TABLE check_rollup_1h RENAME TO rollup_indisponivel_complementar`, `ALTER TABLE rollup_indisponivel_complementar RENAME TO check_rollup_1h`, "42P01", "listar rollups: ", func(t *testing.T, a ambiente, u, id uuid.UUID, broken bool) {
			got, err := a.svc.Checks(context.Background(), u, id, a.clk.Now().Add(-72*time.Hour), a.clk.Now())
			if broken {
				exigirPGComplementar(t, err, "42P01", "listar rollups: ")
			} else if err != nil || len(got.Rollups) != 1 || got.Rollups[0].Total != 2 || got.Rollups[0].OK != 1 {
				t.Fatalf("Rollups não recuperou: %+v, %v", got, err)
			}
		}},
	}
	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			a := novoAmbiente(t)
			u := a.usuario(t)
			id := a.alvo(t, u)
			if tc.name == "Checks_brutos" {
				a.gravarCheck(t, id, a.clk.Now().Add(-time.Minute), "up", 77)
			}
			if tc.name == "Checks_rollups" {
				a.gravarRollup(t, id, a.clk.Now().Add(-time.Hour), 2, 1, ptr(int32(77)))
			}
			restore := alterarDDLComplementar(t, a, tc.apply, tc.undo)
			tc.run(t, a, u, id, true)
			restore()
			original, err := a.svc.Get(context.Background(), u, id)
			if err != nil || original.Name != "API" || original.URL != entrada().URL {
				t.Fatalf("falha alterou alvo: %+v, %v", original, err)
			}
			tc.run(t, a, u, id, false)
		})
	}
}

func TestUpdateTimeoutPersistsAndReschedulesComplementar(t *testing.T) {
	a := novoAmbiente(t)
	u := a.usuario(t)
	id := a.alvo(t, u)
	a.clk.Advance(time.Minute)
	got, err := a.svc.Update(context.Background(), u, id, targets.Patch{TimeoutMS: ptr(int32(5000))})
	if err != nil || got.TimeoutMS != 5000 {
		t.Fatalf("PATCH timeout: %+v, %v", got, err)
	}
	saved, err := a.svc.Get(context.Background(), u, id)
	var next time.Time
	if err != nil || saved.TimeoutMS != 5000 {
		t.Fatalf("timeout não persistiu: %+v, %v", saved, err)
	}
	if err := a.admin.QueryRow(context.Background(), `SELECT next_run_at FROM targets WHERE id=$1`, id).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if !next.Equal(a.clk.Now()) {
		t.Fatalf("timeout não reagendou: %v", next)
	}
}

func TestSummaryExcludesFutureRollupComplementar(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(15 * time.Minute)
	u := a.usuario(t)
	id := a.alvo(t, u)
	h := a.clk.Now().Truncate(time.Hour)
	a.gravarRollup(t, id, h.Add(time.Hour), 10, 0, nil)
	a.gravarCheck(t, id, h.Add(10*time.Minute), "up", 50)
	got := resumoDe(t, a, u)[id]
	if len(got.Spark) != 0 || got.Uptime24h == nil || *got.Uptime24h != 1 {
		t.Fatalf("rollup futuro entrou na janela: %+v", got)
	}
}

func TestChecksRejectsInvalidWindowComplementar(t *testing.T) {
	a := novoAmbiente(t)
	u := a.usuario(t)
	id := a.alvo(t, u)
	for _, span := range []time.Duration{0, time.Second} {
		t.Run(span.String(), func(t *testing.T) {
			got, err := a.svc.Checks(context.Background(), u, id, a.clk.Now().Add(span), a.clk.Now())
			var cause *targets.ValidationError
			if !errors.As(err, &cause) || len(cause.Fields) != 1 || cause.Fields[0].Field != "from" || cause.Fields[0].Reason != targets.ReasonInvalid || len(got.Checks) != 0 || len(got.Rollups) != 0 {
				t.Fatalf("janela vazia/invertida aceita: %+v, %v", got, err)
			}
		})
	}
}

// O tracer somente observa tags públicas e cancela o contexto real do caller.
// Não lê SQL/args nem substitui resultados do driver. A descoberta evita
// codificar número/ordem das consultas da implementação.
type summaryTraceKeyComplementar struct{}
type summaryTraceRequestComplementar struct {
	mu        sync.Mutex
	tags      []string
	stopAfter int
	cancel    context.CancelFunc
	fired     bool
}
type summaryTracerComplementar struct{}

func (summaryTracerComplementar) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (summaryTracerComplementar) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	request, ok := ctx.Value(summaryTraceKeyComplementar{}).(*summaryTraceRequestComplementar)
	if !ok || data.Err != nil {
		return
	}
	request.mu.Lock()
	request.tags = append(request.tags, data.CommandTag.String())
	var cancel context.CancelFunc
	if request.stopAfter > 0 && len(request.tags) == request.stopAfter {
		request.fired = true
		cancel = request.cancel
	}
	request.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func TestSummaryCancellationAndRecoveryComplementar(t *testing.T) {
	a := novoAmbiente(t)
	u := a.usuario(t)
	id := a.alvo(t, u)
	a.gravarCheck(t, id, a.clk.Now().Add(-time.Minute), "up", 80)
	config, err := pgxpool.ParseConfig(testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Tracer = summaryTracerComplementar{}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	svc := targets.NewService(pool, caixa(t), a.clk)
	checkRecovery := func(t *testing.T) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		got, err := svc.Summary(ctx, u)
		if err != nil || len(got) != 1 || got[0].ID != id || got[0].Uptime24h == nil || *got[0].Uptime24h != 1 {
			t.Fatalf("Summary não recuperou: %+v, %v", got, err)
		}
	}
	discovery := &summaryTraceRequestComplementar{}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), summaryTraceKeyComplementar{}, discovery), 10*time.Second)
	got, err := svc.Summary(ctx, u)
	cancel()
	if err != nil || len(got) != 1 || got[0].ID != id {
		t.Fatalf("controle positivo falhou: %+v, %v", got, err)
	}
	discovery.mu.Lock()
	tags := append([]string(nil), discovery.tags...)
	discovery.mu.Unlock()
	if len(tags) == 0 {
		t.Fatal("nenhuma fronteira observada")
	}
	for index, tag := range tags {
		if tag == "COMMIT" {
			continue
		}
		t.Run(fmt.Sprintf("fronteira_%d_%s", index+1, tag), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			request := &summaryTraceRequestComplementar{stopAfter: index + 1, cancel: cancel}
			ctx = context.WithValue(ctx, summaryTraceKeyComplementar{}, request)
			_, err := svc.Summary(ctx, u)
			request.mu.Lock()
			fired := request.fired
			request.mu.Unlock()
			if !fired || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelamento perdido: disparado=%v, erro=%v", fired, err)
			}
			checkRecovery(t)
		})
	}
}
