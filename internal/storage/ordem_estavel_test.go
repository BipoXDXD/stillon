package storage_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Sem desempate, duas linhas empatadas no critério principal saem na ordem
// que o plano do Postgres produzir: a que a heap guardou, a do hash join, a de
// um índice novo. Estes testes empatam o critério principal de propósito e
// exigem a ordem pela chave, que não depende do plano.

// Incidentes que começaram no mesmo instante saem do mais novo para o mais
// antigo pelo id (UUIDv7: a ordem do id é a ordem de criação).
func TestListPublicIncidentsBreaksStartedAtTiesByIncidentIDDescending(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	paginaID := novoID()
	if _, err := conn.Exec(ctx,
		`INSERT INTO status_pages (id, user_id, slug, title, published, created_at) VALUES ($1, $2, 'empate', 'Status', true, $3)`,
		paginaID, dono, agora); err != nil {
		t.Fatalf("criar página: %v", err)
	}
	// O nome de cada serviço diz a que incidente ele pertence; os ids dos
	// incidentes crescem com o índice e a inserção segue a mesma ordem: a
	// ordem que a heap devolveria sem o desempate é a errada de propósito.
	const quantos = 6
	comeco := agora.Add(-2 * time.Hour)
	for i := range quantos {
		alvo := novoAlvo(t, pool, dono, agora)
		if _, err := conn.Exec(ctx,
			`INSERT INTO status_page_items (status_page_id, target_id, display_name, position) VALUES ($1, $2, $3, $4)`,
			paginaID, alvo, fmt.Sprintf("servico-%d", i), i); err != nil {
			t.Fatalf("criar item %d: %v", i, err)
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO incidents (id, target_id, started_at, resolved_at, cause) VALUES ($1, $2, $3, $4, 'timeout')`,
			novoID(), alvo, comeco, comeco.Add(10*time.Minute)); err != nil {
			t.Fatalf("criar incidente %d: %v", i, err)
		}
	}

	linhas, err := db.New(conn).ListPublicIncidents(ctx, db.ListPublicIncidentsParams{
		PageID: paginaID, Since: agora.Add(-24 * time.Hour), MaxRows: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	var nomes []string
	for _, l := range linhas {
		nomes = append(nomes, l.DisplayName)
	}
	quer := []string{"servico-5", "servico-4", "servico-3", "servico-2", "servico-1", "servico-0"}
	if !slices.Equal(nomes, quer) {
		t.Errorf("ordem dos incidentes empatados = %v, queria %v", nomes, quer)
	}
}

// A família viva sai em ordem de family_id; na mesma família, a linha mais
// nova primeiro e, num empate de created_at, a de maior id: quem consome fica
// com a primeira linha de cada família, então o empate não pode ser sorteio.
func TestListLiveFamiliesIsOrderedByFamilyThenNewestRow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	famBaixa, famMedia, famAlta := novoID(), novoID(), novoID()
	instante := agora.Add(-time.Minute)
	insere := func(familia uuid.UUID, criada time.Time, agente string) {
		t.Helper()
		id := novoID()
		if _, err := conn.Exec(ctx,
			`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, created_at, user_agent)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, dono, familia, hashDe(id.String()), agora.Add(time.Hour), criada, agente); err != nil {
			t.Fatalf("criar sessão: %v", err)
		}
	}
	// Inserção do maior family_id para o menor e, na família do meio, do menor
	// id para o maior: o contrário da ordem exigida. A última linha da família
	// do meio é a mais VELHA e tem o MAIOR id: só created_at DESC a põe por
	// último (com ASC, ou só com id DESC, ela subiria).
	insere(famAlta, instante, "alta")
	insere(famMedia, instante, "media-id-menor")
	insere(famMedia, instante, "media-id-maior")
	insere(famBaixa, instante, "baixa")
	insere(famMedia, instante.Add(-time.Hour), "media-velha-id-maior")

	linhas, err := db.New(conn).ListLiveFamilies(ctx, db.ListLiveFamiliesParams{UserID: dono, Now: agora})
	if err != nil {
		t.Fatal(err)
	}
	var obtido []string
	for _, l := range linhas {
		obtido = append(obtido, l.UserAgent)
	}
	quer := []string{"baixa", "media-id-maior", "media-id-menor", "media-velha-id-maior", "alta"}
	if !slices.Equal(obtido, quer) {
		t.Errorf("ordem das famílias vivas = %v, queria %v (family_id, depois created_at e id decrescentes)", obtido, quer)
	}
}

// Quatro alvos com checks em três dias, inseridos de propósito num embaralhado
// que não é a ordem da chave: alvos do maior para o menor e os dias fora de
// ordem. Com 12 grupos em vez de 4, a heap e o hash aggregate do GROUP BY
// quase nunca devolvem a ordem certa por acaso sem o ORDER BY. Os alvos são
// novos, e o id do UUIDv7 cresce: alvos[0] < alvos[1] < alvos[2] < alvos[3].
// Os três instantes (dois dias atrás, ontem e agora) caem no mesmo mês, o da
// partição de agora.
func novosAlvosComChecksEmTresInstantes(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID) (alvos []uuid.UUID, instantes []time.Time) {
	t.Helper()
	for range 4 {
		alvos = append(alvos, novoAlvo(t, pool, dono, agora))
	}
	instantes = []time.Time{agora.Add(-48 * time.Hour), agora.Add(-24 * time.Hour), agora}
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	// Dia do meio, depois o último, depois o primeiro; o alvo 3 antes do 0.
	for _, d := range []int{1, 2, 0} {
		for _, a := range []int{3, 1, 2, 0} {
			novoCheck(t, pool, alvos[a], instantes[d])
		}
	}
	return alvos, instantes
}

// Num banco minúsculo o planner escolhe Sort + GroupAggregate, que entrega os
// grupos já na ordem do GROUP BY: sem o ORDER BY a ordem certa sairia de graça
// e o teste passaria sem provar nada (sabotado, passava). Com o sort
// desencorajado na conexão do teste, o plano cai no hash aggregate, cuja ordem
// de saída é a do hash. O ORDER BY continua ordenando, só custa mais caro.
func desencorajarSort(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), "SET enable_sort = off"); err != nil {
		t.Fatalf("desencorajar o sort: %v", err)
	}
}

// Todas as linhas esperadas, alvo a alvo e, dentro do alvo, do dia mais antigo
// ao mais novo. Os dias vêm literais de quem chama; com "day, target_id" a
// ordem sairia por dia primeiro.
func esperadoPorAlvoEDia(alvos []uuid.UUID, dias []string) []string {
	var quer []string
	for _, a := range alvos {
		for _, d := range dias {
			quer = append(quer, fmt.Sprintf("%s %s", a, d))
		}
	}
	return quer
}

// Primeiro o alvo, depois o dia.
func TestListPublicDailyUptimeIsOrderedByTargetThenDay(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	desencorajarSort(t, conn)
	alvos, instantes := novosAlvosComChecksEmTresInstantes(t, pool, dono)
	paginaID := novoID()
	if _, err := conn.Exec(ctx,
		`INSERT INTO status_pages (id, user_id, slug, title, published, created_at) VALUES ($1, $2, 'ordem-diaria', 'Status', true, $3)`,
		paginaID, dono, agora); err != nil {
		t.Fatalf("criar página: %v", err)
	}
	// Itens do maior alvo para o menor, como os checks.
	for i := len(alvos) - 1; i >= 0; i-- {
		if _, err := conn.Exec(ctx,
			`INSERT INTO status_page_items (status_page_id, target_id, display_name, position) VALUES ($1, $2, $3, $4)`,
			paginaID, alvos[i], fmt.Sprintf("item-%d", i), len(alvos)-i); err != nil {
			t.Fatalf("criar item %d: %v", i, err)
		}
	}

	// Só checks brutos: o corte é o começo da janela, então nenhum rollup entra.
	desde := instantes[0].Add(-time.Hour)
	linhas, err := db.New(conn).ListPublicDailyUptime(ctx, db.ListPublicDailyUptimeParams{
		PageID: paginaID, Since: desde, Cut: desde, Until: instantes[2].Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	var obtido []string
	for _, l := range linhas {
		obtido = append(obtido, fmt.Sprintf("%s %s", l.TargetID, l.Day.UTC().Format(time.DateOnly)))
	}
	quer := esperadoPorAlvoEDia(alvos, []string{"2026-09-20", "2026-09-21", "2026-09-22"})
	if !slices.Equal(obtido, quer) {
		t.Errorf("ordem do uptime diário = %v, queria %v (target_id, depois day)", obtido, quer)
	}
}

// O mesmo para as horas brutas: primeiro o alvo, depois a hora. Este teste
// trava o contrato, mas não pega a remoção do ORDER BY: o percentile_disc é um
// agregado de conjunto ordenado, que não pode ser hasheado, e o plano sempre
// ordena pelas chaves do GROUP BY, que aqui são as mesmas do ORDER BY
// (verificado: sem o ORDER BY o teste continua passando, com ou sem ANALYZE e
// com o sort desencorajado). Só a troca de colunas do ORDER BY o derruba.
func TestListRawHoursIsOrderedByTargetThenBucket(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	alvos, instantes := novosAlvosComChecksEmTresInstantes(t, pool, dono)

	linhas, err := db.New(conn).ListRawHours(ctx, db.ListRawHoursParams{
		UserID: dono, FromAt: instantes[0].Add(-time.Hour), ToAt: instantes[2].Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	var obtido []string
	for _, l := range linhas {
		obtido = append(obtido, fmt.Sprintf("%s %s", l.TargetID, l.Bucket.UTC().Format(time.DateTime)))
	}
	quer := esperadoPorAlvoEDia(alvos, []string{"2026-09-20 12:00:00", "2026-09-21 12:00:00", "2026-09-22 12:00:00"})
	if !slices.Equal(obtido, quer) {
		t.Errorf("ordem das horas brutas = %v, queria %v (target_id, depois bucket)", obtido, quer)
	}
}
