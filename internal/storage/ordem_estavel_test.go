package storage_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

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
	insere := func(familia uuid.UUID, agente string) {
		t.Helper()
		id := novoID()
		if _, err := conn.Exec(ctx,
			`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, created_at, user_agent)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, dono, familia, hashDe(id.String()), agora.Add(time.Hour), instante, agente); err != nil {
			t.Fatalf("criar sessão: %v", err)
		}
	}
	// Inserção do maior family_id para o menor e, no empate da família do
	// meio, do menor id para o maior: o contrário da ordem exigida.
	insere(famAlta, "alta")
	insere(famMedia, "media-id-menor")
	insere(famMedia, "media-id-maior")
	insere(famBaixa, "baixa")

	linhas, err := db.New(conn).ListLiveFamilies(ctx, db.ListLiveFamiliesParams{UserID: dono, Now: agora})
	if err != nil {
		t.Fatal(err)
	}
	var obtido []string
	for _, l := range linhas {
		obtido = append(obtido, l.UserAgent)
	}
	quer := []string{"baixa", "media-id-maior", "media-id-menor", "alta"}
	if !slices.Equal(obtido, quer) {
		t.Errorf("ordem das famílias vivas = %v, queria %v (family_id, depois created_at e id decrescentes)", obtido, quer)
	}
}
