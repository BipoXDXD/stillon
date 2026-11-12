package backup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestURIRejectsSecretsAndPolicyOverrides(t *testing.T) {
	for _, key := range []string{"options", "service", "application_name", "fallback_application_name", "connect_timeout", "password", "sslpassword", "passfile", "oauth_client_secret", "scram_client_key", "scram_server_key"} {
		for _, encoded := range []string{key, strings.ToUpper(key), "%" + map[byte]string{'o': "6f", 's': "73", 'a': "61", 'f': "66", 'c': "63", 'p': "70"}[key[0]] + key[1:]} {
			uri := "postgresql://backup@localhost/stillon?" + encoded + "=SENTINELA-SEGREDO"
			if err := validateURI(uri); err == nil {
				t.Errorf("parâmetro reservado %s foi aceito", encoded)
			} else if strings.Contains(err.Error(), "SENTINELA") || strings.Contains(err.Error(), uri) {
				t.Error("erro expôs credencial/URI")
			}
		}
	}
	for _, uri := range []string{"postgresql://backup:SEGREDO@localhost/stillon", "postgresql://backup:@localhost/stillon", "postgresql://backup@localhost/stillon?%xx=bad", "https://backup@localhost/stillon", "postgresql://backup@localhost/stillon?sslmode=disable\n"} {
		if err := validateURI(uri); err == nil {
			t.Errorf("URI inválida aceita: %q", uri)
		}
	}
}

// Usuário vazio e fragmento passam pelo url.Parse; a recusa é da validação.
func TestURIRejectsEmptyUserAndFragment(t *testing.T) {
	for _, uri := range []string{"postgresql://@localhost/stillon", "postgresql://backup@localhost/stillon#frag"} {
		if err := validateURI(uri); err == nil {
			t.Errorf("URI inválida aceita: %q", uri)
		}
	}
}

func TestURIKeepsLibpqParameters(t *testing.T) {
	for _, uri := range []string{"postgresql://backup@localhost/stillon?sslmode=disable&gssencmode=disable", "postgres://backup@localhost/stillon?target_session_attrs=read-write", "postgresql://backup@/stillon?host=%2Fvar%2Frun%2Fpostgresql", "postgresql://back%75p@localhost/stillon?sslmode=require&channel_binding=prefer"} {
		if err := validateURI(uri); err != nil {
			t.Errorf("parâmetro libpq permitido recusado: %v", err)
		}
	}
}

func TestReferenceRejectsPartialNullOverflowAndExtraFields(t *testing.T) {
	for _, line := range []string{"00000003-0000001A-1||2|3\n", "00000003-0000001A-1|1|2|3", "00000003-0000001A-1|1|2|3|4\n", "00000003-0000001A-1|-1|2|3\n", "00000003-0000001A-1|9223372036854775808|2|3\n", "bad|1|2|3\n"} {
		if _, _, err := parseReference(line); err == nil {
			t.Errorf("referência inválida aceita: %q", line)
		}
	}
	_, ref, err := parseReference("00000003-0000001A-1|21|0|4\n")
	if err != nil || ref != (Reference{21, 0, 4}) {
		t.Fatalf("referência correta: %+v %v", ref, err)
	}
}

// Cada linha abaixo seria aceita sem a regra que o nome diz: o ParseInt aceita
// "+1", e o regex sem âncoras acha o snapshot no meio do lixo.
func TestReferenceRejectsSignsLongLinesAndUnanchoredIDs(t *testing.T) {
	for nome, line := range map[string]string{
		"sinal de mais":      "00000003-0000001A-1|+1|2|3\n",
		"lixo antes do id":   "x00000003-0000001A-1|1|2|3\n",
		"lixo depois do id":  "00000003-0000001A-1x|1|2|3\n",
		"linha acima de 512": strings.Repeat("0", 500) + "3-0000001A-1|1|2|3\n",
	} {
		t.Run(nome, func(t *testing.T) {
			if _, _, err := parseReference(line); err == nil {
				t.Errorf("referência inválida aceita: %q", line)
			}
		})
	}
}

// PGCONNECT_TIMEOUT arredonda para cima, com piso de 1: zero desligaria o prazo.
func TestSourceEnvironmentRoundsTheConnectTimeoutUp(t *testing.T) {
	for _, tc := range []struct {
		prazo time.Duration
		quer  string
	}{
		{0, "PGCONNECT_TIMEOUT=1"},
		{1500 * time.Millisecond, "PGCONNECT_TIMEOUT=2"},
		{2 * time.Second, "PGCONNECT_TIMEOUT=2"},
	} {
		got := ""
		for _, item := range sourceEnv(nil, "s", "a", "o", tc.prazo) {
			if strings.HasPrefix(item, "PGCONNECT_TIMEOUT=") {
				got = item
			}
		}
		if got != tc.quer {
			t.Errorf("prazo %s: %q, queria %q", tc.prazo, got, tc.quer)
		}
	}
}

// Variável PG em minúsculas também fica de fora: o filtro não depende da caixa.
func TestSourceEnvironmentDropsLowercasePGVariables(t *testing.T) {
	env := sourceEnv([]string{"PATH=/fixed", "pgservice=external"}, "s", "a", "o", time.Second)
	for _, item := range env {
		if strings.HasPrefix(item, "pgservice=") {
			t.Fatalf("ambiente herdou %q", item)
		}
	}
}

func TestInputRejectsEmptyOrNULPassword(t *testing.T) {
	for _, senha := range []string{"", "a\x00b"} {
		if err := validarEntrada("postgresql://backup@localhost/stillon", senha, "/tmp/saida"); err == nil {
			t.Errorf("senha %q aceita", senha)
		}
	}
}

// Contexto já cancelado volta antes de tudo, com a causa, e sem criar a saída.
func TestCanceledContextFailsBeforeCreatingTheOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	saida := filepath.Join(t.TempDir(), "dump")
	o := defaultOptions()
	o.protectParent = false
	if _, err := run(ctx, "postgresql://backup@localhost/stillon", "source-secret", saida, o); !errors.Is(err, context.Canceled) {
		t.Fatalf("erro = %v, queria context.Canceled", err)
	}
	if _, err := os.Stat(saida); !os.IsNotExist(err) {
		t.Fatal("contexto cancelado criou a saída")
	}
}

// Os prazos vão ao Postgres em milissegundos, com piso de 1 ms: zero desligaria
// o prazo (statement_timeout=0 é "sem limite").
func TestMillisecondsHasAFloorOfOne(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		quer int64
	}{{0, 1}, {500 * time.Microsecond, 1}, {1500 * time.Microsecond, 1}, {2 * time.Second, 2000}} {
		if got := milissegundos(tc.d); got != tc.quer {
			t.Errorf("milissegundos(%s) = %d, queria %d", tc.d, got, tc.quer)
		}
	}
}

func TestExporterOptionsCarryEveryTimeout(t *testing.T) {
	o := defaultOptions()
	quer := "-c transaction_timeout=600000 -c idle_in_transaction_session_timeout=600000 -c statement_timeout=30000 -c lock_timeout=10000 -c client_connection_check_interval=1000"
	if got := opcoesDoExportador(o); got != quer {
		t.Errorf("opções = %q\nqueria   %q", got, quer)
	}
}

func TestSourceEnvironmentDoesNotInheritRestoreOrPolicy(t *testing.T) {
	env := sourceEnv([]string{"PATH=/fixed", "PGHOST=restore", "PGPASSWORD=restore-secret", "PGOPTIONS=-c client_connection_check_interval=0", "PGSERVICE=external", "PGPASSFILE=/external", "PGAPPNAME=external", "PGSSLKEY=/external"}, "source-secret", "own-run", "-c client_connection_check_interval=1000", time.Second)
	want := map[string]string{"PATH": "/fixed", "PGPASSWORD": "source-secret", "PGAPPNAME": "own-run", "PGOPTIONS": "-c client_connection_check_interval=1000", "PGCONNECT_TIMEOUT": "1"}
	if len(env) != len(want) {
		t.Fatalf("ambiente herdou parâmetros: %v", env)
	}
	for _, item := range env {
		k, v, _ := strings.Cut(item, "=")
		if want[k] != v {
			t.Fatalf("ambiente inesperado: %s", k)
		}
	}
}

// falhaEsperada é o começo da mensagem de cada modo: prova que cada falha sai
// pelo passo certo do supervisor (characterization test da extração do run).
var falhaEsperada = map[string]string{
	"bad-reference":     "referência da origem inválida",
	"overflow":          "protocolo da origem interrompido ou excessivo",
	"sql-error":         "protocolo da origem interrompido ou excessivo",
	"dump-error":        "dump interrompido ou falhou",
	"extra-output":      "fechamento do exportador inválido",
	"exporter-error":    "exportador não encerrou corretamente",
	"remaining-session": "checagem das sessões interrompida",
	"dirty-clear":       "checagem das sessões inválida",
	"blocked-read":      "protocolo da origem interrompido ou excessivo",
	"closed-stdout":     "exportador não encerrou corretamente",
}

func TestSupervisorDoesNotAcceptFailedOrUnclosedClients(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	for _, mode := range []string{"good", "bad-reference", "overflow", "sql-error", "dump-error", "extra-output", "exporter-error", "remaining-session", "dirty-clear", "blocked-read", "closed-stdout"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			psql, dump := clientesFalsos(t, dir)
			t.Setenv("BACKUP_TEST_MODE", mode)
			t.Setenv("PGHOST", "restore")
			t.Setenv("PGPASSWORD", "restore-secret")
			o := opcoesDeTeste(psql, dump)
			if mode == "good" {
				o = comPrazosFolgados(o)
			}
			start := time.Now()
			ref, err := run(context.Background(), "postgresql://backup@localhost/stillon?gssencmode=disable", "source-secret", filepath.Join(dir, "dump"), o)
			if mode == "good" {
				info, statErr := os.Stat(filepath.Join(dir, "dump"))
				if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
					t.Fatalf("artefato não privado/regular: %v %v", info, statErr)
				}
				if err != nil || ref != (Reference{21, 2, 3}) {
					t.Fatalf("sucesso: %+v %v", ref, err)
				}
			} else if err == nil {
				t.Fatalf("%s passou como sucesso: %+v", mode, ref)
			} else if !strings.HasPrefix(err.Error(), falhaEsperada[mode]) {
				t.Fatalf("%s falhou no passo errado: %q, queria começar com %q", mode, err, falhaEsperada[mode])
			}
			if mode != "good" {
				if _, e := os.Stat(filepath.Join(dir, "dump")); !os.IsNotExist(e) {
					t.Fatal("falha deixou artefato próprio")
				}
			}
			if err != nil && (strings.Contains(err.Error(), "source-secret") || strings.Contains(err.Error(), "postgresql://")) {
				t.Fatal("erro vazou credencial")
			}
			if mode == "closed-stdout" && time.Since(start) > 500*time.Millisecond {
				t.Fatalf("Wait do exportador escapou do cleanup: %s", time.Since(start))
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("cancelamento/cleanup presos: %s", elapsed)
			}
		})
	}
}

// clientesFalsos grava psql e pg_dump de mentira em dir. BACKUP_TEST_MODE
// escolhe a falha de cada um.
func clientesFalsos(t *testing.T, dir string) (psql, dump string) {
	t.Helper()
	psql = filepath.Join(dir, "psql")
	dump = filepath.Join(dir, "pg_dump")
	script := `#!/bin/sh
if [ -n "${PGHOST:-}" ] || [ "${PGPASSWORD:-}" != "source-secret" ]; then exit 9; fi
if [ -n "${BACKUP_LOG:-}" ]; then printf 'psql %s\n' "$*" >> "$BACKUP_LOG"; fi
while IFS= read -r line; do
if [ -n "${BACKUP_LOG:-}" ]; then printf 'stdin %s\n' "$line" >> "$BACKUP_LOG"; fi
case "$line" in
 *pg_export_snapshot*)
  case "$BACKUP_TEST_MODE" in
   bad-reference) printf '00000003-0000001A-1||2|3\n';;
   overflow) printf '` + strings.Repeat("x", 600) + `\n';;
   sql-error) printf 'URI postgresql://backup:source-secret@example/stillon\n' >&2; exit 3;;
   blocked-read) :;;
   *) printf '00000003-0000001A-1|21|2|3\n';;
  esac;;
 *pg_stat_clear_snapshot*) if [ "$BACKUP_TEST_MODE" = dirty-clear ]; then printf 'x\n'; else printf '\n'; fi;;
 *pg_stat_activity*) if [ "$BACKUP_TEST_MODE" = remaining-session ]; then printf '1\n'; else if [ "$BACKUP_TEST_MODE" = slow-count ]; then sleep 0.5; fi; printf '0\n'; fi;;
 ROLLBACK*) if [ "$BACKUP_TEST_MODE" = extra-output ]; then printf 'extra\n'; fi;;
 '\q') if [ "$BACKUP_TEST_MODE" = closed-stdout ]; then trap '' TERM; exec 1>&-; exec sleep 60; fi; if [ "$BACKUP_TEST_MODE" = exporter-error ]; then exit 3; fi;exit 0;;
esac
done
exit 0
`
	if err := os.WriteFile(psql, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	dumpScript := "#!/bin/sh\nif [ -n \"${BACKUP_LOG:-}\" ]; then printf 'pg_dump %s\\n' \"$*\" >> \"$BACKUP_LOG\"; fi\ncase \"$BACKUP_TEST_MODE\" in\n dump-error) exit 1;;\n dump-timeout) exec sleep 5;;\nesac\nexit 0\n"
	if err := os.WriteFile(dump, []byte(dumpScript), 0700); err != nil {
		t.Fatal(err)
	}
	return psql, dump
}

// opcoesDeTeste encurta os prazos para os clientes falsos.
// comPrazosFolgados tira o caminho bom do relógio: com os prazos curtos dos
// modos de falha, uma pausa do CI de ~100ms já estourava o encerramento e o
// teste via "file already closed" no lugar do resultado que queria (#314).
func comPrazosFolgados(o options) options {
	o.prepareTimeout, o.dumpTimeout, o.cleanup, o.totalTimeout = 10*time.Second, 10*time.Second, 10*time.Second, 30*time.Second
	return o
}

func opcoesDeTeste(psql, dump string) options {
	o := defaultOptions()
	o.psqlPath = psql
	o.dumpPath = dump
	o.protectParent = false
	o.prepareTimeout = 150 * time.Millisecond
	o.dumpTimeout = 150 * time.Millisecond
	o.totalTimeout = time.Second
	o.grace = 50 * time.Millisecond
	o.cleanup = 100 * time.Millisecond
	return o
}

// A mensagem continua dizendo o passo, mas a causa original
// chega envelopada a quem chama, para o log do job distinguir prazo vencido,
// código de saída do cliente e executável ausente.
func TestFailuresKeepTheOriginalCause(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	saiuCom := func(codigo int) func(error) bool {
		return func(err error) bool {
			var exitErr *exec.ExitError
			return errors.As(err, &exitErr) && exitErr.ExitCode() == codigo
		}
	}
	for _, tc := range []struct {
		modo, causa string
		confere     func(error) bool
		semPsql     bool
	}{
		{modo: "dump-error", causa: "exit status 1 do pg_dump", confere: saiuCom(1)},
		{modo: "exporter-error", causa: "exit status 3 do psql", confere: saiuCom(3)},
		{modo: "dump-timeout", causa: "context.DeadlineExceeded", confere: func(err error) bool { return errors.Is(err, context.DeadlineExceeded) }},
		{modo: "psql-ausente", causa: "fs.ErrNotExist do psql ausente", confere: func(err error) bool { return errors.Is(err, fs.ErrNotExist) }, semPsql: true},
	} {
		t.Run(tc.modo, func(t *testing.T) {
			dir := t.TempDir()
			psql, dump := clientesFalsos(t, dir)
			if tc.semPsql {
				psql = filepath.Join(dir, "psql-ausente")
			}
			t.Setenv("BACKUP_TEST_MODE", tc.modo)
			_, err := run(context.Background(), "postgresql://backup@localhost/stillon", "source-secret", filepath.Join(dir, "dump"), opcoesDeTeste(psql, dump))
			if err == nil || !tc.confere(err) {
				t.Fatalf("erro = %v, queria a causa %s envelopada", err, tc.causa)
			}
			if strings.Contains(err.Error(), "source-secret") || strings.Contains(err.Error(), "postgresql://") {
				t.Fatalf("a causa vazou credencial: %v", err)
			}
		})
	}
}

// Quando a checagem final passa do prazo de encerramento, o prazo fecha os pipes
// e vence a leitura quase juntos, e o erro que chegava primeiro variava (EOF,
// "file already closed", i/o timeout, context canceled). A causa tem de vir
// sempre: o operador precisa ler que o prazo acabou, não um detalhe de pipe.
func TestCleanupDeadlineIsAlwaysReportedAsTheCause(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	for i := range 20 {
		dir := t.TempDir()
		psql, dump := clientesFalsos(t, dir)
		t.Setenv("BACKUP_TEST_MODE", "slow-count")
		saida := filepath.Join(dir, "dump")
		o := opcoesDeTeste(psql, dump)
		o.prepareTimeout, o.dumpTimeout, o.totalTimeout = 5*time.Second, 5*time.Second, 10*time.Second
		_, err := run(context.Background(), "postgresql://backup@localhost/stillon", "source-secret", saida, o)
		if !errors.Is(err, errEncerramentoExcedido) {
			t.Fatalf("rodada %d: erro = %v, queria a causa %q", i, err, errEncerramentoExcedido)
		}
		if _, e := os.Stat(saida); !os.IsNotExist(e) {
			t.Fatalf("rodada %d: falha deixou artefato próprio", i)
		}
	}
}

func TestOutputMustBeFreshAndCannotTruncateSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "valuable")
	if err := os.WriteFile(target, []byte("preservar"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"existing", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(dir, kind)
			if kind == "symlink" {
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("preservar"), 0600); err != nil {
				t.Fatal(err)
			}
			o := defaultOptions()
			o.protectParent = false
			marker := filepath.Join(dir, "started-"+kind)
			client := filepath.Join(dir, "client-"+kind)
			if err := os.WriteFile(client, []byte("#!/bin/sh\nprintf started > \"$BACKUP_MARKER\"\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BACKUP_MARKER", marker)
			o.psqlPath = client
			o.dumpPath = "/missing"
			if _, err := run(context.Background(), "postgresql://backup@localhost/stillon", "source-secret", path, o); err == nil {
				t.Fatal("saída preexistente aceita")
			} else if runtime.GOOS == "linux" && !errors.Is(err, fs.ErrExist) {
				t.Fatalf("erro = %v, queria fs.ErrExist envelopado", err)
			}
			if _, e := os.Stat(marker); !os.IsNotExist(e) {
				t.Fatal("saída existente iniciou cliente antes da recusa")
			}
			if kind == "existing" {
				got, e := os.ReadFile(path)
				if e != nil || string(got) != "preservar" {
					t.Fatalf("saída anterior foi removida/alterada: %q %v", got, e)
				}
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "preservar" {
				t.Fatalf("destino alterado: %q %v", got, err)
			}
		})
	}
}

// Prefixo maior que o protocolo deve falhar sem esperar newline/EOF. A cópia
// sabotada ReadString acumula e espera até o prazo: esse teste acusa a regressão.
func TestProtocolRejectsOversizedPrefixWithoutWaitingForEOF(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	dir := t.TempDir()
	client := filepath.Join(dir, "psql")
	script := "#!/bin/sh\nIFS= read -r line\nIFS= read -r line\nprintf '" + strings.Repeat("x", 1024) + "'\nIFS= read -r line\n"
	if err := os.WriteFile(client, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	o := defaultOptions()
	o.protectParent = false
	o.psqlPath = client
	o.dumpPath = "/missing"
	o.prepareTimeout = 5 * time.Second
	o.totalTimeout = 5 * time.Second
	o.grace = 50 * time.Millisecond
	o.cleanup = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := run(ctx, "postgresql://backup@localhost/stillon", "source-secret", filepath.Join(dir, "dump"), o)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("prefixo excessivo aceito")
		}
	case <-time.After(time.Second):
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cleanup após cancelamento ficou preso")
		}
		t.Fatal("prefixo excessivo esperou newline/EOF")
	}
}

// rodarBom roda o supervisor com os clientes falsos no modo bom, registrando
// em log os argumentos e a entrada de cada cliente. depois roda logo após a
// referência, antes do dump (o gancho afterReference).
func rodarBom(t *testing.T, depois func(saida string) error) (log []string, saida string, err error) {
	t.Helper()
	dir := t.TempDir()
	psql, dump := clientesFalsos(t, dir)
	arquivoLog := filepath.Join(dir, "log")
	t.Setenv("BACKUP_TEST_MODE", "good")
	t.Setenv("BACKUP_LOG", arquivoLog)
	saida = filepath.Join(dir, "dump")
	o := comPrazosFolgados(opcoesDeTeste(psql, dump))
	if depois != nil {
		o.afterReference = func(context.Context, Reference) error { return depois(saida) }
	}
	_, err = run(context.Background(), "postgresql://backup@localhost/stillon", "source-secret", saida, o)
	texto, lerErr := os.ReadFile(arquivoLog)
	if lerErr != nil {
		t.Fatal(lerErr)
	}
	return strings.Split(strings.TrimSuffix(string(texto), "\n"), "\n"), saida, err
}

func linhaQueComeca(log []string, prefixo string) string {
	for _, l := range log {
		if strings.HasPrefix(l, prefixo) {
			return l
		}
	}
	return ""
}

// O pg_dump lê o snapshot exportado, com prazo de lock; o psql para no
// primeiro erro e nunca pede senha no terminal.
func TestClientsReceiveTheSnapshotAndSafetyArguments(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	log, _, err := rodarBom(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	dump, psql := linhaQueComeca(log, "pg_dump "), linhaQueComeca(log, "psql ")
	for _, arg := range []string{"--snapshot=00000003-0000001A-1", "--lock-wait-timeout=10000", "--no-password"} {
		if !strings.Contains(dump, " "+arg+" ") {
			t.Errorf("pg_dump sem %s: %q", arg, dump)
		}
	}
	for _, arg := range []string{"--no-password", "ON_ERROR_STOP=1"} {
		if !strings.Contains(psql, " "+arg+" ") {
			t.Errorf("psql sem %s: %q", arg, psql)
		}
	}
}

// A referência sai de uma transação REPEATABLE READ somente leitura, e a
// contagem de sessões da execução não conta a própria conexão do exportador.
func TestExporterQueriesPinTheSnapshotAndSkipItsOwnSession(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	log, _, err := rodarBom(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := linhaQueComeca(log, "stdin BEGIN"); got != "stdin BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;" {
		t.Errorf("abertura da transação = %q", got)
	}
	if got := linhaQueComeca(log, "stdin SELECT count(*) FROM pg_stat_activity"); !strings.HasSuffix(got, " AND pid<>pg_backend_pid();") {
		t.Errorf("contagem de sessões = %q, queria excluir a própria conexão", got)
	}
}

// Um erro do gancho depois da referência para tudo antes do dump.
func TestAfterReferenceErrorStopsBeforeTheDump(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	log, saida, err := rodarBom(t, func(string) error { return errors.New("parar") })
	if err == nil || !strings.HasPrefix(err.Error(), "preparação interrompida") {
		t.Fatalf("erro = %v, queria a preparação interrompida", err)
	}
	if linhaQueComeca(log, "pg_dump ") != "" {
		t.Error("o dump rodou depois do erro do gancho")
	}
	if _, e := os.Stat(saida); !os.IsNotExist(e) {
		t.Error("falha deixou artefato próprio")
	}
}

// A saída com a permissão alterada durante a execução não é aceita.
func TestOutputWithChangedPermissionIsRejected(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	_, _, err := rodarBom(t, func(saida string) error { return os.Chmod(saida, 0644) })
	if err == nil || !strings.HasPrefix(err.Error(), "saída da execução foi substituída ou alterada") {
		t.Fatalf("erro = %v, queria a saída recusada", err)
	}
}

// A saída trocada por outro arquivo durante a execução não é aceita, e o
// descarte não apaga o arquivo que não é desta execução. O original é
// renomeado, e não apagado, para que o substituto não reaproveite o inode.
func TestReplacedOutputIsRejectedAndTheImpostorKept(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	_, saida, err := rodarBom(t, func(saida string) error {
		if err := os.Rename(saida, saida+".original"); err != nil {
			return err
		}
		return os.WriteFile(saida, []byte("alheio"), 0600)
	})
	if err == nil || !strings.HasPrefix(err.Error(), "saída da execução foi substituída ou alterada") {
		t.Fatalf("erro = %v, queria a saída recusada", err)
	}
	if got, e := os.ReadFile(saida); e != nil || string(got) != "alheio" {
		t.Fatalf("o descarte mexeu no arquivo alheio: %q %v", got, e)
	}
}
