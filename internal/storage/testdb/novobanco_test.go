package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// NovoBanco dá a cada teste um banco só dele, já migrado: o que um grava, o
// outro não vê, mesmo rodando ao mesmo tempo. É isso que deixa os testes de
// um pacote usarem t.Parallel() sem dividir o estado do snapshot.
func TestNovoBancoIsolaOsTestes(t *testing.T) {
	ctx := context.Background()
	var nomes [2]string
	for i, nome := range []string{"primeiro", "segundo"} {
		t.Run(nome, func(t *testing.T) {
			pool := NovoBanco(t)
			if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&nomes[i]); err != nil {
				t.Fatal(err)
			}
			// Já migrado, e vazio: o mesmo e-mail (único na tabela) entra
			// nos dois. Num banco dividido, o segundo insert violaria o UNIQUE.
			if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES (gen_random_uuid(), 'isolado@exemplo.com', 'x')`); err != nil {
				t.Fatalf("insert no banco novo: %v", err)
			}
		})
	}
	if nomes[0] == "" || nomes[0] == nomes[1] {
		t.Fatalf("os dois testes usaram o mesmo banco (%q e %q)", nomes[0], nomes[1])
	}
}

// DSN e AdminDSN, chamados no teste que pediu NovoBanco, apontam para o banco
// dele, e não para o compartilhado: um teste que abre um pool à parte (o
// admin que trava uma tabela, o pool de sessões que ele derruba) mexe no
// mesmo banco que a API do teste usa. O papel continua o de cada DSN: o da
// aplicação, sem superusuário, e o admin.
func TestDSNsSeguemONovoBanco(t *testing.T) {
	ctx := context.Background()
	pool := NovoBanco(t)
	var meu string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&meu); err != nil {
		t.Fatal(err)
	}
	for nome, c := range map[string]struct {
		dsn       string
		superuser bool
	}{"DSN": {DSN(t), false}, "AdminDSN": {AdminDSN(t), true}} {
		conn, err := pgx.Connect(ctx, c.dsn)
		if err != nil {
			t.Fatalf("%s: %v", nome, err)
		}
		var banco string
		var super bool
		if err := conn.QueryRow(ctx, `SELECT current_database(), current_setting('is_superuser') = 'on'`).Scan(&banco, &super); err != nil {
			t.Fatalf("%s: %v", nome, err)
		}
		_ = conn.Close(ctx)
		if banco != meu {
			t.Errorf("%s aponta para %q, queria o banco do teste, %q", nome, banco, meu)
		}
		if super != c.superuser {
			t.Errorf("%s: superusuário = %v, queria %v", nome, super, c.superuser)
		}
	}
}

// O banco do teste some no fim dele: 266 testes por pacote não podem deixar
// 266 bancos para trás no contêiner.
func TestNovoBancoSomeNoFimDoTeste(t *testing.T) {
	ctx := context.Background()
	var nome string
	t.Run("usa", func(t *testing.T) {
		if err := NovoBanco(t).QueryRow(ctx, `SELECT current_database()`).Scan(&nome); err != nil {
			t.Fatal(err)
		}
	})
	conn, err := pgx.Connect(ctx, AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var existe bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, nome).Scan(&existe); err != nil {
		t.Fatal(err)
	}
	if existe {
		t.Fatalf("o banco %q ficou depois do teste", nome)
	}
}

// Os pools dos testes em paralelo cabem no contêiner: com o padrão do
// Postgres (100), uma máquina de 10 núcleos ou mais esgotaria as conexões.
func TestContainerAguentaOsPoolsEmParalelo(t *testing.T) {
	ctx := context.Background()
	var valor string
	if err := NovoBanco(t).QueryRow(ctx, `SHOW max_connections`).Scan(&valor); err != nil {
		t.Fatal(err)
	}
	if valor != "400" {
		t.Fatalf("max_connections = %s, queria 400", valor)
	}
}
