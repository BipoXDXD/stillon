package storage_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const sufixoDasFaixas = "heartbeat_limits"

// Faixas da migration heartbeat_limits, e as de antes dela, que a descida
// recoloca. Literais calculados à mão: 365 d, 31 d e 7 d em segundos.
const (
	tetoNovo           = 31536000
	tetoAntigoPeriodo  = 2678400
	tetoAntigoTolera   = 604800
	descricaoNovaMax   = 10000
	descricaoAntigaMax = 1000
)

// linhaDeHeartbeat é uma linha crua de heartbeats: os testes de faixa gravam
// direto no banco, sem passar pela validação da API.
type linhaDeHeartbeat struct {
	periodo   *int32
	cron      *string
	grace     int32
	descricao string
	prazo     time.Time
}

func inserirLinha(ctx context.Context, tx pgx.Tx, dono uuid.UUID, l linhaDeHeartbeat) (uuid.UUID, error) {
	id := novoID()
	var fuso *string
	if l.cron != nil {
		utc := "UTC"
		fuso = &utc
	}
	_, err := tx.Exec(ctx, `INSERT INTO heartbeats
		(id, user_id, name, ping_token_hash, period_sec, cron_expr, timezone, grace_sec,
		 description, created_at, deadline_at)
		VALUES ($1, $2, 'h', $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, dono, hashDe(id.String()), l.periodo, l.cron, fuso, l.grace, l.descricao, agora, l.prazo)
	return id, err
}

func TestHeartbeatLimitsChecksAcceptTheNewEdges(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	periodo := func(s int32) *int32 { return &s }
	casos := []struct {
		nome           string
		linha          linhaDeHeartbeat
		codigoEsperado string
	}{
		{"período no teto", linhaDeHeartbeat{periodo: periodo(tetoNovo), grace: 300}, ""},
		{"período acima do teto", linhaDeHeartbeat{periodo: periodo(tetoNovo + 1), grace: 300}, "23514"},
		{"período abaixo do piso", linhaDeHeartbeat{periodo: periodo(59), grace: 300}, "23514"},
		{"tolerância no teto", linhaDeHeartbeat{periodo: periodo(3600), grace: tetoNovo}, ""},
		{"tolerância acima do teto", linhaDeHeartbeat{periodo: periodo(3600), grace: tetoNovo + 1}, "23514"},
		{"tolerância abaixo do piso", linhaDeHeartbeat{periodo: periodo(3600), grace: 29}, "23514"},
		// 'é' tem dois bytes: o teto é de caracteres, não de bytes.
		{"descrição no teto", linhaDeHeartbeat{periodo: periodo(3600), grace: 300,
			descricao: strings.Repeat("é", descricaoNovaMax)}, ""},
		{"descrição acima do teto", linhaDeHeartbeat{periodo: periodo(3600), grace: 300,
			descricao: strings.Repeat("é", descricaoNovaMax+1)}, "23514"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			c.linha.prazo = agora
			err := emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
				_, err := inserirLinha(ctx, tx, dono, c.linha)
				return err
			})
			if codigoPG(err) != c.codigoEsperado {
				t.Errorf("deu %v, queria código %q", err, c.codigoEsperado)
			}
		})
	}
}

// A descida converte em vez de apagar: o binário do piso lê
// essas linhas, e quem desce não perde a tarefa.
func TestHeartbeatLimitsDownConvertsAndRestoresTheOldChecks(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoDasFaixas)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dono := novoUsuario(t, pool)

	longo, normal := int32(tetoNovo), int32(3600)
	expr := "0 3 * * *"
	// Um ano à frente: a descida tem de trazê-lo para dentro dos tetos antigos.
	daquiAUmAno := time.Now().UTC().AddDate(1, 0, 0).Truncate(time.Second)
	prazoCron := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	// Prazo já próximo (no passado): reduzir o período não pode adiá-lo.
	prazoPerto := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var idLonga, idCron, idNormal, idPerto, idToleraLonga uuid.UUID
	err = emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		var err error
		if idLonga, err = inserirLinha(ctx, tx, dono, linhaDeHeartbeat{periodo: &longo, grace: tetoNovo,
			descricao: strings.Repeat("é", descricaoNovaMax), prazo: daquiAUmAno}); err != nil {
			return err
		}
		if idCron, err = inserirLinha(ctx, tx, dono, linhaDeHeartbeat{cron: &expr, grace: 1209600,
			prazo: prazoCron}); err != nil {
			return err
		}
		if idNormal, err = inserirLinha(ctx, tx, dono, linhaDeHeartbeat{periodo: &normal, grace: 300,
			descricao: "backup", prazo: prazoCron}); err != nil {
			return err
		}
		if idPerto, err = inserirLinha(ctx, tx, dono, linhaDeHeartbeat{periodo: &longo, grace: 300,
			prazo: prazoPerto}); err != nil {
			return err
		}
		idToleraLonga, err = inserirLinha(ctx, tx, dono, linhaDeHeartbeat{periodo: &normal, grace: tetoNovo,
			prazo: daquiAUmAno})
		return err
	})
	if err != nil {
		t.Fatalf("gravar linhas nas faixas novas: %v", err)
	}

	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("descer a %d: %v", versao-1, err)
	}

	type lida struct {
		periodo       int32 // 0 é "sem período" (cron)
		grace         int32
		caracteres    int
		prazo         time.Time
		prazoNaJanela bool
	}
	// ler confere também se o prazo caiu em [now() + segundos - 1 min, now() + segundos]:
	// os dois lados, para que nem "um ano à frente" nem "agora" passem.
	ler := func(id uuid.UUID, segundos int32) lida {
		t.Helper()
		var l lida
		err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT COALESCE(period_sec, 0), grace_sec, char_length(description), deadline_at,
				deadline_at BETWEEN now() + make_interval(secs => $2::int) - interval '1 minute'
				                AND now() + make_interval(secs => $2::int)
				FROM heartbeats WHERE id = $1`, id, segundos).
				Scan(&l.periodo, &l.grace, &l.caracteres, &l.prazo, &l.prazoNaJanela)
		})
		if err != nil {
			t.Fatalf("ler %s depois da descida: %v", id, err)
		}
		return l
	}

	// 3283200 s = 31 d de período + 7 d de tolerância, os tetos antigos.
	if l := ler(idLonga, 3283200); l.periodo != tetoAntigoPeriodo || l.grace != tetoAntigoTolera ||
		l.caracteres != descricaoAntigaMax || !l.prazoNaJanela {
		t.Errorf("linha longa ficou período %d, tolerância %d, %d caracteres, prazo %s (em now()+38 d: %v); "+
			"queria 2678400, 604800, 1000 e prazo em now()+38 d", l.periodo, l.grace, l.caracteres, l.prazo, l.prazoNaJanela)
	}
	// 608400 s = 1 h de período + 7 d de tolerância.
	if l := ler(idToleraLonga, 608400); l.periodo != 3600 || l.grace != tetoAntigoTolera || !l.prazoNaJanela {
		t.Errorf("linha de tolerância longa ficou período %d, tolerância %d, prazo %s (em now()+608400 s: %v); "+
			"queria 3600, 604800 e prazo em now()+608400 s", l.periodo, l.grace, l.prazo, l.prazoNaJanela)
	}
	if l := ler(idCron, 0); l.periodo != 0 || l.grace != tetoAntigoTolera || !l.prazo.Equal(prazoCron) {
		t.Errorf("linha cron ficou período %d, tolerância %d, prazo %s; queria sem período, 604800 e %s",
			l.periodo, l.grace, l.prazo, prazoCron)
	}
	if l := ler(idNormal, 0); l.periodo != 3600 || l.grace != 300 || l.caracteres != len("backup") ||
		!l.prazo.Equal(prazoCron) {
		t.Errorf("linha normal mudou: período %d, tolerância %d, %d caracteres, prazo %s",
			l.periodo, l.grace, l.caracteres, l.prazo)
	}
	if l := ler(idPerto, 0); l.periodo != tetoAntigoPeriodo || !l.prazo.Equal(prazoPerto) {
		t.Errorf("linha longa de prazo próximo ficou período %d, prazo %s; queria 2678400 e %s",
			l.periodo, l.prazo, prazoPerto)
	}

	acima := int32(tetoAntigoPeriodo + 1)
	err = emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		_, err := inserirLinha(ctx, tx, dono, linhaDeHeartbeat{periodo: &acima, grace: 300, prazo: agora})
		return err
	})
	if codigoPG(err) != "23514" {
		t.Errorf("período 2678401 depois da descida deu %v, queria 23514", err)
	}

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Errorf("subir de novo à %d: %v", versao, err)
	}
}

// O piso de rollback (o binário anterior) sobe sobre o schema com as faixas
// novas: as migrations dele já estão aplicadas, e o boot é um no-op.
func TestFloorBinaryBootsOnHeartbeatLimits(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	piso, err := goose.NewProvider(goose.DialectPostgres, conn,
		migrationsAte(t, arquivoDaMigration(t, sufixoDasFaixas)))
	if err != nil {
		t.Fatal(err)
	}
	aplicadas, err := piso.Up(ctx)
	if err != nil {
		t.Fatalf("boot do piso: %v", err)
	}
	if len(aplicadas) != 0 {
		t.Errorf("o piso aplicou %d migrations", len(aplicadas))
	}
}
