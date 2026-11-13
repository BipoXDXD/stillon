package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Os testes deste arquivo chamam InsertStatusPageItem direto, sem o
// statuspage.Service: provam que o banco segura as regras dos itens mesmo
// quando a validação do serviço falha ou é contornada.

// inserirItem grava um item na página do dono, sob a RLS do dono.
func inserirItem(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, item db.InsertStatusPageItemParams) error {
	t.Helper()
	ctx := context.Background()
	return storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertStatusPageItem(ctx, item)
	})
}

// conectarComoAdmin abre uma conexão de superusuário, que ignora a RLS: sem
// ela, o WITH CHECK da política barraria a página ou o alvo inexistente com
// 42501 antes de a FK ser consultada.
func conectarComoAdmin(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatalf("conectar como admin: %v", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func TestStatusPageItemRejectsRepeatedPosition(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo1, alvo2 := novoAlvo(t, pool, dono, agora), novoAlvo(t, pool, dono, agora)
	pagina := novaPagina(t, pool, dono, "pagina-a", false, alvo1)

	err := inserirItem(t, pool, dono, db.InsertStatusPageItemParams{
		StatusPageID: pagina, TargetID: alvo2, DisplayName: "Outro", Position: 0,
	})
	if codigoPG(err) != "23505" {
		t.Errorf("posição 0 repetida deu %v, queria 23505", err)
	}
}

func TestStatusPageItemRejectsRepeatedTarget(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	pagina := novaPagina(t, pool, dono, "pagina-a", false, alvo)

	err := inserirItem(t, pool, dono, db.InsertStatusPageItemParams{
		StatusPageID: pagina, TargetID: alvo, DisplayName: "De novo", Position: 1,
	})
	if codigoPG(err) != "23505" {
		t.Errorf("mesmo alvo duas vezes na página deu %v, queria 23505", err)
	}
}

// A mesma posição em páginas diferentes é permitida: a unicidade é por
// página, não global.
func TestStatusPageItemPositionIsUniquePerPage(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novaPagina(t, pool, a, "pagina-a", false, novoAlvo(t, pool, a, agora))
	paginaB := novaPagina(t, pool, b, "pagina-b", false)

	err := inserirItem(t, pool, b, db.InsertStatusPageItemParams{
		StatusPageID: paginaB, TargetID: novoAlvo(t, pool, b, agora), DisplayName: "B", Position: 0,
	})
	if err != nil {
		t.Errorf("posição 0 na página de B, já usada na de A, deu %v, queria nil", err)
	}
}

func TestStatusPageItemPositionMustNotBeNegative(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	pagina := novaPagina(t, pool, dono, "pagina-a", false)

	casos := []struct {
		posicao int32
		codigo  string
	}{
		{posicao: -1, codigo: "23514"},
		{posicao: 0, codigo: ""},
	}
	for _, c := range casos {
		err := inserirItem(t, pool, dono, db.InsertStatusPageItemParams{
			StatusPageID: pagina, TargetID: novoAlvo(t, pool, dono, agora), DisplayName: "Serviço", Position: c.posicao,
		})
		if codigoPG(err) != c.codigo || (c.codigo == "" && err != nil) {
			t.Errorf("posição %d deu %v, queria código %q", c.posicao, err, c.codigo)
		}
	}
}

func TestStatusPageItemDisplayNameLength(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	pagina := novaPagina(t, pool, dono, "pagina-a", false)

	casos := []struct {
		nome   string
		codigo string
	}{
		{nome: "", codigo: "23514"},
		{nome: "a", codigo: ""},
		{nome: strings.Repeat("a", 100), codigo: ""},
		{nome: strings.Repeat("a", 101), codigo: "23514"},
		// char_length conta caracteres, não bytes: 100 "é" são 200 bytes.
		{nome: strings.Repeat("é", 100), codigo: ""},
	}
	for i, c := range casos {
		err := inserirItem(t, pool, dono, db.InsertStatusPageItemParams{
			StatusPageID: pagina, TargetID: novoAlvo(t, pool, dono, agora), DisplayName: c.nome, Position: int32(i),
		})
		if codigoPG(err) != c.codigo || (c.codigo == "" && err != nil) {
			t.Errorf("nome com %d caracteres deu %v, queria código %q", len([]rune(c.nome)), err, c.codigo)
		}
	}
}

func TestStatusPageItemRequiresExistingPageAndTarget(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	pagina := novaPagina(t, pool, dono, "pagina-a", false)
	q := db.New(conectarComoAdmin(t))
	ctx := context.Background()

	err := q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
		StatusPageID: novoID(), TargetID: alvo, DisplayName: "Sem página", Position: 0,
	})
	if codigoPG(err) != "23503" {
		t.Errorf("página inexistente deu %v, queria 23503", err)
	}
	err = q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
		StatusPageID: pagina, TargetID: novoID(), DisplayName: "Sem alvo", Position: 0,
	})
	if codigoPG(err) != "23503" {
		t.Errorf("alvo inexistente deu %v, queria 23503", err)
	}
}

// Não há rota que apague só a página; ela some com o dono. Os itens vão junto
// (ON DELETE CASCADE), e não sobram órfãos apontando para uma página morta.
func TestDeletingTheStatusPageDeletesItsItems(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	pagina := novaPagina(t, pool, dono, "pagina-a", false, novoAlvo(t, pool, dono, agora), novoAlvo(t, pool, dono, agora))
	admin := conectarComoAdmin(t)
	ctx := context.Background()

	if _, err := admin.Exec(ctx, `DELETE FROM status_pages WHERE id = $1`, pagina); err != nil {
		t.Fatalf("apagar página: %v", err)
	}
	var n int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM status_page_items WHERE status_page_id = $1`, pagina).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sobraram %d itens da página apagada, queria 0", n)
	}
}
