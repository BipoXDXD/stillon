package backup

import (
	"context"
	"os"
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
	"blocked-read":      "protocolo da origem interrompido ou excessivo",
	"closed-stdout":     "exportador não encerrou corretamente",
}

func TestSupervisorDoesNotAcceptFailedOrUnclosedClients(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("supervisor exige Linux")
	}
	for _, mode := range []string{"good", "bad-reference", "overflow", "sql-error", "dump-error", "extra-output", "exporter-error", "remaining-session", "blocked-read", "closed-stdout"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			psql := filepath.Join(dir, "psql")
			dump := filepath.Join(dir, "pg_dump")
			script := `#!/bin/sh
if [ -n "${PGHOST:-}" ] || [ "${PGPASSWORD:-}" != "source-secret" ]; then exit 9; fi
while IFS= read -r line; do
case "$line" in
 *pg_export_snapshot*)
  case "$BACKUP_TEST_MODE" in
   bad-reference) printf '00000003-0000001A-1||2|3\n';;
   overflow) printf '` + strings.Repeat("x", 600) + `\n';;
   sql-error) printf 'URI postgresql://backup:source-secret@example/stillon\n' >&2; exit 3;;
   blocked-read) :;;
   *) printf '00000003-0000001A-1|21|2|3\n';;
  esac;;
 *pg_stat_clear_snapshot*) printf '\n';;
 *pg_stat_activity*) if [ "$BACKUP_TEST_MODE" = remaining-session ]; then printf '1\n'; else printf '0\n'; fi;;
 ROLLBACK*) if [ "$BACKUP_TEST_MODE" = extra-output ]; then printf 'extra\n'; fi;;
 '\q') if [ "$BACKUP_TEST_MODE" = closed-stdout ]; then trap '' TERM; exec 1>&-; exec sleep 60; fi; if [ "$BACKUP_TEST_MODE" = exporter-error ]; then exit 3; fi;exit 0;;
esac
done
exit 0
`
			if err := os.WriteFile(psql, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dump, []byte("#!/bin/sh\nif [ \"$BACKUP_TEST_MODE\" = dump-error ]; then exit 1; fi\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BACKUP_TEST_MODE", mode)
			t.Setenv("PGHOST", "restore")
			t.Setenv("PGPASSWORD", "restore-secret")
			o := defaultOptions()
			o.psqlPath = psql
			o.dumpPath = dump
			o.protectParent = false
			o.prepareTimeout = 150 * time.Millisecond
			o.dumpTimeout = 150 * time.Millisecond
			o.totalTimeout = time.Second
			o.grace = 50 * time.Millisecond
			o.cleanup = 100 * time.Millisecond
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
