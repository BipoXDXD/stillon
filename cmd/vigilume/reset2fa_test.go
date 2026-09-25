package main

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// donoCom2FA cria o dono pelo CLI e liga o 2FA direto no banco (segredo,
// confirmação, timestep, uma troca pendente e três códigos de recuperação),
// com duas sessões vivas. O CLI não tem a chave mestra, e o reset-2fa não
// precisa dela: só zera colunas.
func donoCom2FA(t *testing.T, pool *pgxpool.Pool, dsn string, ligar bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if r := rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Fatalf("create: código %d; %s", r.codigo, r.stderr)
	}
	var dono uuid.UUID
	var hash string
	if err := pool.QueryRow(ctx, `SELECT id, password_hash FROM users`).Scan(&dono, &hash); err != nil {
		t.Fatal(err)
	}
	if ligar {
		if _, err := pool.Exec(ctx,
			`UPDATE users SET totp_secret_enc = '\x01', totp_confirmed_at = now(), totp_last_timestep = 7,
			        totp_pending_secret_enc = '\x02', totp_pending_family_id = gen_random_uuid() WHERE id = $1`, dono); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if _, err := pool.Exec(ctx,
				`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
				uuid.New(), dono, uuid.New().String()); err != nil {
				t.Fatal(err)
			}
		}
	}
	mgr := auth.NewManager(auth.NewPGStore(pool), clock.Real{}, []byte("chave-de-teste-com-32-bytes-ok!!"))
	for range 2 {
		if _, _, err := mgr.Issue(ctx, dono, auth.PasswordStamp(hash), auth.Meta{IP: "203.0.113.9"}); err != nil {
			t.Fatal(err)
		}
	}
	return dono
}

func vivasDe(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND used_at IS NULL AND revoked_at IS NULL`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// reset-2fa com o 2FA ligado: sai 0, desliga o 2FA (inclusive a troca
// pendente), apaga os códigos, encerra todas as sessões e audita. Não lê senha
// nenhuma: o stdin é um leitor que nunca responde, e o comando termina assim
// mesmo.
func TestReset2FATurnsItOffAndEndsEverySession(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	dono := donoCom2FA(t, pool, dsn, true)

	r := rodarUserCom(t, dsn, entradaDaSenha{pipe: novoLeitorParado(t)}, nil, "reset-2fa", "--email", "dono@exemplo.com")
	if r.codigo != saidaOK {
		t.Fatalf("reset-2fa: código %d; stderr %q", r.codigo, r.stderr)
	}
	queria := "2FA desativado para dono@exemplo.com; 2 sessões encerradas. Acessos já abertos expiram em até 15 minutos. Entre com a senha e ative de novo pelo painel.\n"
	if r.stdout != queria {
		t.Errorf("stdout = %q, queria %q", r.stdout, queria)
	}

	var ligado, sobra bool
	var codigos int
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_confirmed_at IS NOT NULL,
		        totp_secret_enc IS NOT NULL OR totp_last_timestep IS NOT NULL OR totp_pending_secret_enc IS NOT NULL
		           OR totp_pending_family_id IS NOT NULL,
		        (SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1)
		   FROM users WHERE id = $1`, dono).Scan(&ligado, &sobra, &codigos); err != nil {
		t.Fatal(err)
	}
	if ligado || sobra {
		t.Errorf("o 2FA ficou: confirmado=%v, sobrou coluna=%v", ligado, sobra)
	}
	if codigos != 0 {
		t.Errorf("sobraram %d códigos de recuperação", codigos)
	}
	if v := vivasDe(t, pool, dono); v != 0 {
		t.Errorf("%d sessões continuaram vivas", v)
	}
	if n := auditorias(t, pool, "totp.reset_cli"); n != 1 {
		t.Errorf("%d registros totp.reset_cli, queria 1", n)
	}
}

// Sem 2FA: sai 0, diz que nada mudou, e não encerra sessão nem audita.
func TestReset2FAWhenAlreadyOffChangesNothing(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	dono := donoCom2FA(t, pool, dsn, false)

	r := rodarUser(t, dsn, "", "reset-2fa", "--email", "dono@exemplo.com")
	if r.codigo != saidaOK {
		t.Fatalf("reset-2fa: código %d; stderr %q", r.codigo, r.stderr)
	}
	queria := "o 2FA já estava desativado para dono@exemplo.com; nada mudou\n"
	if r.stdout != queria {
		t.Errorf("stdout = %q, queria %q", r.stdout, queria)
	}
	if v := vivasDe(t, pool, dono); v != 2 {
		t.Errorf("%d sessões vivas, queria 2: o reset-2fa sem 2FA encerrou sessões", v)
	}
	if n := auditorias(t, pool, "totp.reset_cli"); n != 0 {
		t.Errorf("%d registros totp.reset_cli sem 2FA ligado", n)
	}
}

// Os códigos de saída são os do set-password: 4 para e-mail não cadastrado
// (nada muda), 2 para uso errado. O texto de uso lista o subcomando.
func TestReset2FAExitCodes(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	dono := donoCom2FA(t, pool, dsn, true)

	if r := rodarUser(t, dsn, "", "reset-2fa", "--email", "ninguem@exemplo.com"); r.codigo != saidaNaoCadastrado {
		t.Errorf("e-mail desconhecido: código %d, queria 4; stderr %q", r.codigo, r.stderr)
	} else if !strings.Contains(r.stderr, "ninguem@exemplo.com") {
		t.Errorf("stderr não diz qual e-mail faltou: %q", r.stderr)
	}
	for nome, args := range map[string][]string{
		"sem --email":       {"reset-2fa"},
		"argumento a mais":  {"reset-2fa", "--email", "dono@exemplo.com", "extra"},
		"e-mail sem arroba": {"reset-2fa", "--email", "dono"},
	} {
		if r := rodarUser(t, dsn, "", args...); r.codigo != saidaUso {
			t.Errorf("%s: código %d, queria 2", nome, r.codigo)
		}
	}
	if r := rodarUser(t, "", "", "reset-2fa", "--email", "dono@exemplo.com"); r.codigo != saidaUso {
		t.Errorf("sem DATABASE_URL: código %d, queria 2", r.codigo)
	}
	// Nenhuma das recusas mexeu em nada: a perna positiva é o teste acima.
	if v := vivasDe(t, pool, dono); v != 2 {
		t.Errorf("uma recusa encerrou sessões (%d vivas)", v)
	}
	if n := auditorias(t, pool, "totp.reset_cli"); n != 0 {
		t.Errorf("uma recusa auditou %d reset(s)", n)
	}

	r := rodarUser(t, dsn, "")
	if r.codigo != saidaUso || !strings.Contains(r.stderr, "vigilume user reset-2fa --email <e-mail>") {
		t.Errorf("o uso não lista o reset-2fa (código %d): %q", r.codigo, r.stderr)
	}
}
