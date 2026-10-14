package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// O hook existe somente no executável de teste compilado em checkout
// temporário. O core de produção, libpq, dump, restauro e SQL são reais.
const snapshotPrivateHarness = `package backup
import("context";"fmt";"os";"os/signal";"path/filepath";"strings";"syscall";"testing";"time")
func TestMain(m *testing.M) {
 if len(os.Args)!=2 || os.Args[1]!="backup-snapshot" { os.Exit(m.Run()) }
 if os.Geteuid()!=65532 { fmt.Fprintln(os.Stderr,"fixture: helper não perdeu root"); os.Exit(1) }
 dir:=filepath.Dir(os.Getenv("SAIDA"))
 if e:=os.WriteFile(dir+"/helper.pid",[]byte(fmt.Sprint(os.Getpid())),0644);e!=nil{fmt.Fprintln(os.Stderr,e);os.Exit(1)}
 status,_:=os.ReadFile("/proc/self/status")
 if !strings.Contains(string(status),"CapEff:\t0000000000000000") || !strings.Contains(string(status),"NoNewPrivs:\t1") {fmt.Fprintln(os.Stderr,"fixture: caps/NNP incorretos");os.Exit(1)}
 o:=defaultOptions();o.prepareTimeout=10*time.Second;o.dumpTimeout=15*time.Second;o.totalTimeout=30*time.Second;o.grace=time.Second;o.cleanup=3*time.Second
 o.psqlPath="/test/bin/psql-source";o.dumpPath="/test/bin/pg_dump-source"
 os.Setenv("PGSERVICE","servico-inexistente-do-restauro");os.Setenv("PGSERVICEFILE","/test/nenhum-service.conf")
 o.afterReference=func(ctx context.Context,r Reference)error {
  dir:=filepath.Dir(os.Getenv("SAIDA"))
  if e:=os.WriteFile(dir+"/reference",[]byte(fmt.Sprintf("%d|%d|%d",r.Version,r.Users,r.Targets)),0600);e!=nil{return e}
  for {if _,e:=os.Stat(dir+"/release");e==nil{return nil};select{case<-ctx.Done():return ctx.Err();case<-time.After(10*time.Millisecond):}}
 }
 ctx,cancel:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM);defer cancel()
 r,e:=run(ctx,os.Getenv("STILLON_ORIGEM_PUBLICA"),os.Getenv("PGPASSWORD"),os.Getenv("SAIDA"),o)
 if e!=nil {fmt.Fprintln(os.Stderr,"fixture:",e);os.Exit(1)}
 fmt.Printf("%s|%d|%d|%d\n",Protocol,r.Version,r.Users,r.Targets);os.Exit(0)
}
`

func TestBackupSnapshotConcurrentChangesAndRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("precisa de PostgreSQL 18 e Docker")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal("integração de backup precisa de Docker")
	}
	// Um pull frio escreve progresso no stderr e somente o ID no stdout.
	// Exercite essa fronteira mesmo com a imagem já presente no host: o
	// launcher dá EXEC ao Docker real, sem fabricar ID ou resultado SQL.
	launcher := t.TempDir()
	body := "#!/bin/sh\nif [ \"${1:-}\" = run ]; then printf '%s\\n' 'fixture: progresso de pull no stderr' >&2; fi\nexec '" + strings.ReplaceAll(docker, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(launcher, "docker"), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", launcher+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	buildSnapshotFixture(t, ctx, root, dir)
	for _, sub := range []string{"bin", "deploy/backup", "deploy/compose", "cases"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"backup/backup.sh", "backup/verificar.sql", "compose/initdb.sql"} {
		body, err := os.ReadFile(filepath.Join(root, "deploy", file))
		if err != nil {
			t.Fatal(err)
		}
		if file == "backup/backup.sh" {
			if err := os.WriteFile(filepath.Join(dir, "deploy/backup/backup-real.sh"), []byte(strings.ReplaceAll(string(body), "/stillon backup-snapshot", "/test/helper-real backup-snapshot")), 0644); err != nil {
				t.Fatal(err)
			}
			body = []byte(strings.ReplaceAll(string(body), "/stillon backup-snapshot", "/test/helper-test backup-snapshot"))
		}
		if err := os.WriteFile(filepath.Join(dir, "deploy", file), body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// 0755 como o runbook instala; ver TestInitDBAppliesLiteralPasswords.
	copySnapshotFile(t, filepath.Join(root, "deploy/vps/initdb.sh"), filepath.Join(dir, "initdb.sh"), 0755)
	if err := os.WriteFile(filepath.Join(dir, "pg_hba.conf"), []byte("local all all trust\nhost all all 127.0.0.1/32 scram-sha-256\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Wrappers observam argv/ambiente e dão EXEC ao cliente real. Nunca
	// fabricam referência, resultado SQL ou sucesso de pg_dump/pg_restore.
	for _, tool := range []string{"psql", "pg_dump"} {
		body := fmt.Sprintf("#!/bin/sh\nd=${SAIDA%%/*}\nprintf '%%s\\n' \"$@\" >> \"$d/source.argv\"\nprintf '%%s' \"$PGPASSWORD\" > \"$d/source.password\"\nenv | sed -n '/^PG[A-Z_]*=/p' > \"$d/source.env\"\nexec /usr/local/bin/%s \"$@\"\n", tool)
		if err := os.WriteFile(filepath.Join(dir, "bin", tool+"-source"), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	restoreWrapper := `#!/bin/sh
/usr/local/bin/pg_restore "$@" || exit $?
if [ "${SNAPSHOT_TEST_DAMAGE:-}" = 1 ]; then
 /usr/local/bin/psql -XqAt -v ON_ERROR_STOP=1 -d stillon -c 'BEGIN; DELETE FROM targets WHERE id=1; COMMIT;'
fi
`
	if err := os.WriteFile(filepath.Join(dir, "bin/pg_restore"), []byte(restoreWrapper), 0755); err != nil {
		t.Fatal(err)
	}
	image := snapshotImage(t, root)
	password := "público%:@'\"\\ +$()&?#;"
	restorePassword := "restauro-sentinela-diferente"
	env := map[string]string{"POSTGRES_PASSWORD": "admin-sentinela", "STILLON_APP_PASSWORD": "app-sentinela", "STILLON_BACKUP_PASSWORD": password}
	source := snapshotContainer(t, ctx, env, "--network", "none", "--init", "--security-opt", "no-new-privileges:true", "-v", dir+":/test", "-v", filepath.Join(dir, "initdb.sh")+":/docker-entrypoint-initdb.d/01-stillon.sh:ro", image, "-c", "hba_file=/test/pg_hba.conf")
	snapshotReady(t, ctx, source, "5432")
	restore := snapshotContainer(t, ctx, map[string]string{"POSTGRES_PASSWORD": restorePassword}, "--network", "container:"+source, "--init", "--security-opt", "no-new-privileges:true", "--memory", "3g", "--tmpfs", "/var/lib/postgresql:rw,size=2g", "-v", dir+":/test", image, "-p", "5433")
	snapshotReady(t, ctx, restore, "5433")
	t.Cleanup(func() {
		// A fixture exercita UID65532, mas TempDir precisa recuperar seus
		// próprios arquivos no host antes que o container seja removido.
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := snapshotExec(cleanup, restore, nil, "sh", "-c", `chown -R "$1:$2" /test/cases; chmod -R u+rwX /test/cases`, "fixture", strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("devolver arquivos exclusivos da fixture ao host: %v\n%s", err, out)
		}
	})
	host := snapshotDocker(t, ctx, nil, "inspect", restore, "--format", "{{json .HostConfig}}")
	var limits struct {
		Memory int64
		Tmpfs  map[string]string
	}
	if err := json.Unmarshal([]byte(host), &limits); err != nil {
		t.Fatal(err)
	}
	if limits.Memory != 3*1024*1024*1024 || !strings.Contains(limits.Tmpfs["/var/lib/postgresql"], "size=2g") {
		t.Fatalf("limites do restauro alterados: %+v", limits)
	}
	sourceSQL := func(sql string) string {
		return snapshotSQL(t, ctx, source, "5432", "postgres", "admin-sentinela", "stillon", sql)
	}
	wrong := snapshotExec(ctx, source, map[string]string{"PGPASSWORD": "senha-incorreta-publica"}, "/usr/local/bin/psql", "-XqAt", "--no-password", "-h", "127.0.0.1", "-U", "stillon_backup", "-d", "stillon", "-c", "SELECT 1;")
	wrongOut, wrongErr := wrong.CombinedOutput()
	if wrongErr == nil || !strings.Contains(string(wrongOut), "password authentication failed") {
		t.Fatalf("fixture não exige SCRAM de verdade: %v\n%s", wrongErr, wrongOut)
	}
	restoreSQL := func(db, sql string) string {
		return snapshotSQL(t, ctx, restore, "5433", "postgres", restorePassword, db, sql)
	}
	roles := sourceSQL("SELECT rolsuper,rolbypassrls,rolcreatedb,rolcreaterole,pg_has_role(oid,'pg_read_all_data','MEMBER') FROM pg_roles WHERE rolname='stillon_backup';")
	if roles != "f|t|f|f|t" {
		t.Fatalf("papel backup não reproduz produção: %s", roles)
	}
	sourceSQL(`SET ROLE stillon_app;
CREATE TABLE goose_db_version(version_id bigint); INSERT INTO goose_db_version VALUES(11);
CREATE TABLE users(id int PRIMARY KEY); INSERT INTO users VALUES(1);
CREATE TABLE targets(id int PRIMARY KEY,user_id int REFERENCES users(id));
CREATE TABLE gate(id int);
CREATE TABLE checks(id int,at date) PARTITION BY RANGE(at);
CREATE TABLE checks_2026 PARTITION OF checks FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
ALTER TABLE users ENABLE ROW LEVEL SECURITY; ALTER TABLE users FORCE ROW LEVEL SECURITY; CREATE POLICY todos ON users USING(true) WITH CHECK(true);
ALTER TABLE targets ENABLE ROW LEVEL SECURITY; ALTER TABLE targets FORCE ROW LEVEL SECURITY; CREATE POLICY todos ON targets USING(true) WITH CHECK(true);
ALTER TABLE checks ENABLE ROW LEVEL SECURITY; ALTER TABLE checks FORCE ROW LEVEL SECURITY; CREATE POLICY todos ON checks USING(true) WITH CHECK(true);
ALTER TABLE checks_2026 ENABLE ROW LEVEL SECURITY; ALTER TABLE checks_2026 FORCE ROW LEVEL SECURITY; CREATE POLICY todos ON checks_2026 USING(true) WITH CHECK(true);`)
	credentials := url.UserPassword("stillon_backup", password).String()
	origin := "postgresql://" + credentials + "@127.0.0.1:5432/stillon?sslmode=disable&gssencmode=disable"
	_, encodedPassword, _ := strings.Cut(credentials, ":")
	public := "postgresql://stillon_backup@127.0.0.1:5432/stillon?sslmode=disable&gssencmode=disable"
	for index, c := range []struct {
		name, change string
		damage       bool
		code         int
		realCLI      bool
	}{
		{name: "controle", code: 0},
		{name: "exclusão concorrente", change: "DELETE FROM targets WHERE id=2;", code: 0},
		{name: "inclusão concorrente", change: "INSERT INTO targets VALUES(3,1);", code: 0},
		{name: "remoção artificial", damage: true, code: 3},
		{name: "inclusão não mascara remoção", change: "INSERT INTO targets VALUES(3,1);", damage: true, code: 3},
		{name: "disco preserva cópia", code: 4},
		{name: "CLI definitivo sem hook", code: 0, realCLI: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			caseCtx, stopCase := context.WithTimeout(ctx, 40*time.Second)
			defer stopCase()
			sourceSQL("BEGIN; TRUNCATE targets; INSERT INTO targets VALUES(1,1),(2,1); COMMIT;")
			restoreSQL("postgres", "DROP DATABASE IF EXISTS stillon; DROP ROLE IF EXISTS stillon_app;")
			casePath := "/test/cases/" + strconv.Itoa(index)
			snapshotDocker(t, ctx, nil, "exec", restore, "sh", "-c", `mkdir -p "$1"; chown 0:65532 "$1"; chmod 0770 "$1"`, "fixture", casePath)
			childEnv := map[string]string{"ORIGEM_URL": origin, "SAIDA": casePath + "/dump", "PGHOST": "127.0.0.1", "PGPORT": "5433", "PGUSER": "postgres", "PGPASSWORD": restorePassword, "PGSERVICE": "servico-inexistente-do-restauro", "PGSERVICEFILE": "/test/nenhum-service.conf", "PGOPTIONS": "-c client_connection_check_interval=0", "PATH": "/test/bin:/usr/local/bin:/usr/bin:/bin"}
			// PGSERVICE quebraria o próprio restauro, que não está sendo mudado.
			// O helper limpa os PG*; para testar essa limpeza sem contaminar os
			// clientes do restauro, o hook TESTONLY injeta os valores somente nele.
			delete(childEnv, "PGSERVICE")
			delete(childEnv, "PGSERVICEFILE")
			if c.damage {
				childEnv["SNAPSHOT_TEST_DAMAGE"] = "1"
			}
			if c.code == 4 {
				childEnv["VOLUME_BYTES"] = "1"
			}
			script := "/test/deploy/backup/backup.sh"
			if c.realCLI {
				script = "/test/deploy/backup/backup-real.sh"
			}
			cmd := snapshotExec(caseCtx, restore, childEnv, "sh", script)
			var output strings.Builder
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			finished := false
			defer func() {
				if finished {
					return
				}
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				// Só o PID publicado pelo próprio helper desta fixture. O
				// executável é conferido para não sinalizar um PID reciclado.
				kill := snapshotExec(cleanup, restore, nil, "sh", "-c", `p=$(cat "$1/helper.pid" 2>/dev/null) || exit 0; [ "$(readlink "/proc/$p/exe")" = /test/helper-test ] && kill -TERM "$p" || :`, "fixture", casePath)
				_ = kill.Run()
				_ = cmd.Process.Kill()
				select {
				case <-done:
				case <-cleanup.Done():
					t.Error("comando da fixture não encerrou na limpeza")
				}
			}()
			// Wait early termination explicitly, instead of polling a missing
			// barrier forever when credentials/protocol fail.
			reference := ""
			deadline := time.Now().Add(12 * time.Second)
			for !c.realCLI && reference == "" && time.Now().Before(deadline) {
				select {
				case err := <-done:
					finished = true
					t.Fatalf("backup terminou antes da referência: %v\n%s", err, output.String())
				default:
				}
				read := snapshotExec(ctx, restore, nil, "cat", casePath+"/reference")
				bytes, err := read.Output()
				if err == nil {
					reference = strings.TrimSpace(string(bytes))
				} else {
					time.Sleep(30 * time.Millisecond)
				}
			}
			if !c.realCLI && reference != "11|1|2" {
				_ = cmd.Process.Kill()
				<-done
				finished = true
				t.Fatalf("referência/barreira inválida: %q\n%s", reference, output.String())
			}
			if !c.realCLI {
				held := sourceSQL("SELECT count(*) FROM pg_stat_activity WHERE usename='stillon_backup' AND backend_xmin IS NOT NULL AND state='idle in transaction';")
				if held != "1" {
					t.Fatalf("exportador não mantém snapshot durante barreira: %s", held)
				}
			}
			if c.change != "" {
				sourceSQL("BEGIN; SET LOCAL ROLE stillon_app; " + c.change + " COMMIT;")
			}
			if !c.realCLI {
				snapshotDocker(t, ctx, nil, "exec", restore, "touch", casePath+"/release")
			}
			err := <-done
			finished = true
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !asSnapshotExit(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != c.code {
				t.Fatalf("backup código%d, esperado%d\n%s", code, c.code, output.String())
			}
			restored := restoreSQL("stillon", "SELECT string_agg(id::text,',' ORDER BY id) FROM targets;")
			expected := "1,2"
			if c.damage {
				expected = "2"
			}
			if restored != expected {
				t.Fatalf("IDs completos restaurados %q; esperado%q", restored, expected)
			}
			rowsSQL := `SELECT (SELECT string_agg(id::text||':'||user_id::text,',' ORDER BY id) FROM targets),(SELECT string_agg(id::text,',' ORDER BY id) FROM users),(SELECT max(version_id) FROM goose_db_version);`
			expectedRows := "1:1,2:1|1|11"
			if c.damage {
				expectedRows = "2:1|1|11"
			}
			if rows := restoreSQL("stillon", rowsSQL); rows != expectedRows {
				t.Fatalf("dados/vínculos/versão restaurados %q; esperado %q", rows, expectedRows)
			}
			restoreSQL("postgres", "CREATE DATABASE inspeccao OWNER stillon_app;")
			snapshotDocker(t, ctx, map[string]string{"PGPASSWORD": restorePassword}, "exec", "-e", "PGPASSWORD", restore, "/usr/local/bin/pg_restore", "--exit-on-error", "-h", "127.0.0.1", "-p", "5433", "-U", "postgres", "-d", "inspeccao", casePath+"/dump")
			fileIDs := restoreSQL("inspeccao", "SELECT string_agg(id::text,',' ORDER BY id) FROM targets;")
			if fileIDs != "1,2" {
				t.Fatalf("dump não usou a referência: IDs%q", fileIDs)
			}
			if rows := restoreSQL("inspeccao", rowsSQL); rows != "1:1,2:1|1|11" {
				t.Fatalf("dados/vínculos/versão do dump mudaram: %q", rows)
			}
			facts := restoreSQL("inspeccao", `SELECT (SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tableowner<>'stillon_app'),(SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relrowsecurity AND NOT c.relforcerowsecurity),(SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relrowsecurity),(SELECT count(*) FROM pg_inherits WHERE inhparent='checks'::regclass);`)
			if facts != "0|0|4|1" {
				t.Fatalf("owners/FORCE/RLS/partições mudaram no dump: %s", facts)
			}
			restoreSQL("postgres", "DROP DATABASE inspeccao;")
			if !c.realCLI {
				argv := snapshotDocker(t, ctx, nil, "exec", restore, "cat", casePath+"/source.argv")
				if strings.Contains(argv, password) || strings.Contains(argv, encodedPassword) {
					t.Error("senha entrou no argv real de clientes")
				}
				if !strings.Contains(argv, public) || !strings.Contains(argv, "--snapshot=") {
					t.Error("URI libpq preservada/snapshot ausente dos clientes reais")
				}
				gotPass := snapshotDocker(t, ctx, nil, "exec", restore, "cat", casePath+"/source.password")
				if gotPass != password {
					t.Error("senha especial não chegou literalmente ao cliente real")
				}
				envRecord := snapshotDocker(t, ctx, nil, "exec", restore, "cat", casePath+"/source.env")
				if strings.Contains(envRecord, "PGPORT=") || strings.Contains(envRecord, "PGHOST=") || strings.Contains(envRecord, "PGUSER=") || strings.Contains(envRecord, "PGSERVICE=") {
					t.Errorf("ambiente do restauro vazou ao cliente origem: %s", envRecord)
				}
				if !strings.Contains(envRecord, "PGAPPNAME=stillon_snapshot_") || !strings.Contains(envRecord, "PGCONNECT_TIMEOUT=10") {
					t.Error("identidade/budget próprios ausentes")
				}
			}
			if left := sourceSQL("SELECT count(*) FROM pg_stat_activity WHERE usename='stillon_backup';"); left != "0" {
				t.Fatalf("sessões backup permaneceram: %s", left)
			}
		})
	}
	t.Run("SIGKILL do shell com gosu e CLI definitivos", func(t *testing.T) {
		snapshotKillRealChain(t, ctx, source, restore, origin, restorePassword, sourceSQL, restoreSQL)
	})
}

// A referência não lê gate. Esse lock bloqueia somente o dump depois de
// importar o snapshot, evitando fabricar sucesso por EOF ou por soltar DDL.
func snapshotKillRealChain(t *testing.T, parent context.Context, source, restore, origin, restorePassword string, sourceSQL func(string) string, restoreSQL func(string, string) string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	restoreSQL("postgres", "DROP DATABASE IF EXISTS stillon; DROP ROLE IF EXISTS stillon_app;")
	casePath := "/test/cases/kill"
	snapshotDocker(t, ctx, nil, "exec", restore, "sh", "-c", `mkdir -p "$1"; chown 0:65532 "$1"; chmod 0770 "$1"`, "fixture", casePath)
	holder := snapshotExec(ctx, source, map[string]string{"PGPASSWORD": "admin-sentinela"}, "/usr/local/bin/psql", "-XqAt", "--no-password", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-U", "postgres", "-d", "stillon")
	in, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = fmt.Fprintln(in, "ROLLBACK;")
		_ = in.Close()
		if err := holder.Wait(); err != nil && ctx.Err() == nil {
			t.Errorf("encerrar dono da barreira: %v", err)
		}
	}()
	if f, ok := out.(*os.File); ok {
		_ = f.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	_, _ = fmt.Fprintln(in, "BEGIN; LOCK TABLE gate IN ACCESS EXCLUSIVE MODE; SELECT pg_backend_pid();")
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("barreira SQL de lock: %v", err)
	}
	holderPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ORIGEM_URL": origin, "SAIDA": casePath + "/dump", "PGHOST": "127.0.0.1", "PGPORT": "5433", "PGUSER": "postgres", "PGPASSWORD": restorePassword}
	cmd := snapshotExec(ctx, restore, env, "sh", "-c", `printf '%s\n' "$$" > "$1/shell.pid"; exec sh /test/deploy/backup/backup-real.sh`, "fixture", casePath)
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-ctx.Done():
				t.Error("comando da cadeia real não encerrou")
			}
		}
	}()
	backend := ""
	limit := time.Now().Add(8 * time.Second)
	for backend == "" && time.Now().Before(limit) {
		select {
		case err := <-done:
			finished = true
			t.Fatalf("cadeia encerrou antes de importar snapshot: %v\n%s", err, output.String())
		default:
		}
		backend = sourceSQL(`SELECT pid::text || '|' || backend_xmin::text FROM pg_stat_activity WHERE usename='stillon_backup' AND application_name LIKE 'stillon_snapshot_%_dump' AND wait_event_type='Lock' AND wait_event='relation' AND backend_xmin IS NOT NULL;`)
		if backend == "" {
			time.Sleep(30 * time.Millisecond)
		}
	}
	if backend == "" {
		t.Fatal("pg_dump real não chegou ao lock com snapshot retido")
	}
	shellText := snapshotDocker(t, ctx, nil, "exec", restore, "cat", casePath+"/shell.pid")
	shellPID, err := strconv.Atoi(shellText)
	if err != nil {
		t.Fatal(err)
	}
	processes := snapshotDocker(t, ctx, nil, "exec", restore, "ps", "-o", "pid,ppid,args")
	helperPID, dumpPID := 0, 0
	for _, row := range strings.Split(processes, "\n") {
		fields := strings.Fields(row)
		if len(fields) >= 4 && fields[1] == strconv.Itoa(shellPID) && fields[2] == "/test/helper-real" && fields[3] == "backup-snapshot" {
			helperPID, _ = strconv.Atoi(fields[0])
		}
	}
	for _, row := range strings.Split(processes, "\n") {
		fields := strings.Fields(row)
		if len(fields) >= 3 && fields[1] == strconv.Itoa(helperPID) && fields[2] == "/usr/local/bin/pg_dump" && strings.Contains(row, "--snapshot=") {
			dumpPID, _ = strconv.Atoi(fields[0])
		}
	}
	if helperPID == 0 || dumpPID == 0 {
		t.Fatalf("cadeia direta shell→gosu EXEC→CLI→dump não identificada: %s", processes)
	}
	status := snapshotDocker(t, ctx, nil, "exec", restore, "cat", fmt.Sprintf("/proc/%d/status", helperPID))
	if !strings.Contains(status, "Uid:\t65532\t65532\t65532\t65532") || !strings.Contains(status, "CapEff:\t0000000000000000") || !strings.Contains(status, "NoNewPrivs:\t1") {
		t.Fatal("CLI real não executou como UID65532/caps0/NNP")
	}
	snapshotDocker(t, ctx, nil, "exec", restore, "kill", "-KILL", strconv.Itoa(shellPID))
	select {
	case err := <-done:
		finished = true
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 137 {
			t.Fatalf("SIGKILL shell não foi observado: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cadeia não encerrou depois de SIGKILL no shell")
	}
	limit = time.Now().Add(5 * time.Second)
	left := ""
	for time.Now().Before(limit) {
		left = sourceSQL("SELECT count(*) FROM pg_stat_activity WHERE usename='stillon_backup';")
		if left == "0" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if left != "0" {
		t.Fatalf("backend permaneceu após morrer cadeia real, com gate ainda fechado: %s", left)
	}
	if held := sourceSQL(fmt.Sprintf("SELECT count(*) FROM pg_locks WHERE pid=%d AND relation='gate'::regclass AND mode='AccessExclusiveLock' AND granted;", holderPID)); held != "1" {
		t.Fatal("o teste soltou a barreira antes de comprovar SQL0")
	}
	for _, pid := range []int{helperPID, dumpPID} {
		if err := snapshotExec(ctx, restore, nil, "kill", "-0", strconv.Itoa(pid)).Run(); err == nil {
			t.Errorf("processo da cadeia ainda existe: PID%d", pid)
		}
	}
	if strings.Contains(output.String(), "backup conferido") {
		t.Fatal("cancelamento da cadeia foi anunciado como backup válido")
	}
	if created := restoreSQL("postgres", "SELECT count(*) FROM pg_database WHERE datname='stillon';"); created != "0" {
		t.Fatal("restauro avançou depois do cancelamento da cadeia")
	}
	t.Logf("CLI UID65532/caps0/NNP: shell %d, helper %d, pg_dump %d; backend %s desapareceu com gate ainda fechado", shellPID, helperPID, dumpPID, backend)
}

func asSnapshotExit(err error, out **exec.ExitError) bool {
	exit, ok := err.(*exec.ExitError)
	if ok {
		*out = exit
	}
	return ok
}

func buildSnapshotFixture(t *testing.T, ctx context.Context, root, dir string) {
	t.Helper()
	module := filepath.Join(dir, "module")
	pkg := filepath.Join(module, "internal/backup")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"go.mod", "go.sum"} {
		copySnapshotFile(t, filepath.Join(root, file), filepath.Join(module, file), 0644)
	}
	entries, err := os.ReadDir(filepath.Join(root, "internal/backup"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			copySnapshotFile(t, filepath.Join(root, "internal/backup", entry.Name()), filepath.Join(pkg, entry.Name()), 0644)
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "harness_test.go"), []byte(snapshotPrivateHarness), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-mod=readonly", "-buildvcs=false", "-o", filepath.Join(dir, "helper-test"), "./internal/backup")
	cmd.Dir = module
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compilar core/hook de teste: %v\n%s", err, out)
	}
	real := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-buildvcs=false", "-o", filepath.Join(dir, "helper-real"), "./cmd/stillon")
	real.Dir = root
	real.Env = cmd.Env
	if out, err := real.CombinedOutput(); err != nil {
		t.Fatalf("compilar CLI real: %v\n%s", err, out)
	}
}
func copySnapshotFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, body, mode); err != nil {
		t.Fatal(err)
	}
}
func snapshotImage(t *testing.T, root string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "deploy/vps/db/Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "FROM ") {
			return strings.TrimPrefix(line, "FROM ")
		}
	}
	t.Fatal("Dockerfile sem imagemPG18")
	return ""
}
func snapshotExec(ctx context.Context, cid string, env map[string]string, args ...string) *exec.Cmd {
	all := []string{"exec", "-i"}
	for key := range env {
		all = append(all, "-e", key)
	}
	all = append(all, cid)
	all = append(all, args...)
	cmd := exec.CommandContext(ctx, "docker", all...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd
}
func snapshotDocker(t *testing.T, ctx context.Context, env map[string]string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	// Progresso de pull e avisos pertencem ao stderr; nunca podem entrar
	// no ID do contêiner nem nos dados que o chamador recebe do stdout.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Docker fixture: %v\n%s\n%s", err, out, stderr.String())
	}
	return strings.TrimSuffix(string(out), "\n")
}
func snapshotContainer(t *testing.T, ctx context.Context, env map[string]string, args ...string) string {
	t.Helper()
	name := fmt.Sprintf("stillon-snapshot-contract-%d", time.Now().UnixNano())
	all := []string{"run", "--rm", "-d", "--name", name}
	for key := range env {
		all = append(all, "-e", key)
	}
	cid := strings.TrimSpace(snapshotDocker(t, ctx, env, append(all, args...)...))
	t.Logf("contêiner descartável %s: %s", name, cid)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cleanup, "docker", "rm", "-f", "-v", cid)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("limpar container exclusivo %s: %v\n%s", cid, err, out)
		}
		check := exec.CommandContext(cleanup, "docker", "ps", "-aq", "--no-trunc", "--filter", "id="+cid)
		if out, err := check.Output(); err != nil || strings.TrimSpace(string(out)) != "" {
			t.Errorf("contêiner da fixture permaneceu após limpeza: %v\n%s", err, out)
		}
	})
	return cid
}
func snapshotReady(t *testing.T, ctx context.Context, cid, port string) {
	t.Helper()
	limit := time.Now().Add(40 * time.Second)
	for time.Now().Before(limit) {
		if snapshotExec(ctx, cid, nil, "pg_isready", "-h", "127.0.0.1", "-p", port, "-U", "postgres").Run() == nil {
			return
		}
		if ctx.Err() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("PostgreSQL descartável não ficou pronto")
}
func snapshotSQL(t *testing.T, ctx context.Context, cid, port, role, password, db, sql string) string {
	t.Helper()
	cmd := snapshotExec(ctx, cid, map[string]string{"PGPASSWORD": password}, "/usr/local/bin/psql", "-XqAt", "--no-password", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", port, "-U", role, "-d", db)
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("SQL de fixture: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
