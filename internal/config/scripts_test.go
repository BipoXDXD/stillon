package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Reintroduzir -v senha=... no init ou a URI com senha no backup deve falhar:
// os executáveis falsos só observam a fronteira real entre shell e cliente PG.
func TestInitDBKeepsPasswordsOutOfArguments(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, filepath.Join(dir, "psql"), `#!/bin/sh
printf '%s\n' "$@" > "$REGISTRO/argv"
cat > "$REGISTRO/stdin"
`)
	app, backup := "sentinela-publica-app'\\aspas", "sentinela-publica-backup'\\aspas"
	cmd := exec.Command("sh", "../../deploy/vps/initdb.sh")
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "REGISTRO="+dir,
		"STILLON_APP_PASSWORD="+app, "STILLON_BACKUP_PASSWORD="+backup)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("initdb: %v\n%s", err, out)
	}
	argv := readScriptRecord(t, dir, "argv")
	for _, secret := range []string{app, backup} {
		if strings.Contains(argv, secret) || strings.Contains(string(out), secret) {
			t.Error("senha de teste entrou em argv ou saída do initdb")
		}
	}
	if readScriptRecord(t, dir, "stdin") == "" {
		t.Fatal("initdb não entregou SQL ao psql")
	}
}

func TestBackupKeepsSourcePasswordOutOfArguments(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, filepath.Join(dir, "psql"), `#!/bin/sh
set -eu
printf '%s\n' "$@" >> "$REGISTRO/argv"
case "$*" in
 *goose_db_version*) echo 1 ;;
 *'count(*) FROM users'*) echo 1 ;;
 *'count(*) FROM targets'*) echo 0 ;;
 *pg_database_size*)
   printf 'consulta\n' >> "$REGISTRO/size-calls"
   printf '%s' "$PGPASSWORD" > "$REGISTRO/size-password"
   env | sed -n '/^PG[A-Z_]*=/p' > "$REGISTRO/size-env"
   echo 1234 ;;
esac
`)
	writeScript(t, filepath.Join(dir, "env"), `#!/bin/sh
set -eu
if [ "${1:-}" = -i ]; then printf '%s\n' "$@" >> "$REGISTRO/argv"; fi
exec /usr/bin/env "$@"
`)
	writeScript(t, filepath.Join(dir, "pg_dump"), `#!/bin/sh
printf '%s\n' "$@" >> "$REGISTRO/argv"
printf '%s' "$PGPASSWORD" > "$REGISTRO/password"
for arg; do case "$arg" in --file=*) echo publico > "${arg#--file=}" ;; esac; done
`)
	writeScript(t, filepath.Join(dir, "pg_restore"), "#!/bin/sh\nprintf '%s' \"$PGPASSWORD\" > \"$REGISTRO/restore-password\"\n")
	helper := filepath.Join(dir, "snapshot-helper")
	writeScript(t, helper, `#!/bin/sh
set -eu
printf '%s\n' "$@" >> "$REGISTRO/argv"
printf '%s' "$PGPASSWORD" > "$REGISTRO/password"
printf '%s' "$STILLON_ORIGEM_PUBLICA" > "$REGISTRO/helper-uri"
[ "$#" -eq 1 ] && [ "$1" = backup-snapshot ] || exit 1
echo publico > "$SAIDA"
printf 'stillon-backup-snapshot-v1|1|1|0\n'
`)
	script := backupScriptWithHelper(t, dir, helper)
	for _, c := range []struct{ name, encoded, password string }{
		{"simples", "sentinela-publica-backup", "sentinela-publica-backup"},
		{"escapes", "sentinela%27%5C%3A%40%25%20fim", "sentinela'\\:@% fim"},
		{"unicode", "p%C3%BAblico%25%3A%40%27%22%5C%20%2B%24%28%29%26%3F%23%3B", "público%:@'\"\\ +$()&?#;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, file := range []string{"argv", "size-calls", "size-password", "size-env"} {
				_ = os.Remove(filepath.Join(dir, file))
			}
			cmd := exec.Command("sh", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "REGISTRO="+dir,
				"ORIGEM_URL=postgres://stillon_backup:"+c.encoded+"@127.0.0.1:5432/stillon?sslmode=disable",
				"PGHOST=127.0.0.1", "PGPASSWORD=restauro-publico", "PGSERVICE=sentinela-servico-restauro", "PGOPTIONS=-c statement_timeout=0", "SAIDA="+filepath.Join(dir, "dump"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("backup: %v\n%s", err, out)
			}
			argv := readScriptRecord(t, dir, "argv")
			for _, secret := range []string{c.encoded, c.password} {
				if strings.Contains(argv, secret) || strings.Contains(string(out), secret) {
					t.Error("senha da origem entrou em argv ou saída do backup")
				}
			}
			if readScriptRecord(t, dir, "password") != c.password {
				t.Error("helper não recebeu a credencial da origem pelo ambiente")
			}
			if readScriptRecord(t, dir, "helper-uri") != "postgres://stillon_backup@127.0.0.1:5432/stillon?sslmode=disable" {
				t.Error("helper não recebeu a URI pública preservada")
			}
			if readScriptRecord(t, dir, "restore-password") != "restauro-publico" {
				t.Error("senha de origem substituiu a senha do restauro")
			}
			if readScriptRecord(t, dir, "size-calls") != "consulta\n" || readScriptRecord(t, dir, "size-password") != c.password {
				t.Error("consulta final de disco não recebeu uma vez a senha literal da origem")
			}
			sizeEnv := readScriptRecord(t, dir, "size-env")
			if strings.Contains(sizeEnv, "PGHOST=") || strings.Contains(sizeEnv, "PGSERVICE=") || strings.Contains(sizeEnv, "statement_timeout=0") {
				t.Error("consulta final de disco herdou PG* do restauro")
			}
			if !strings.Contains(sizeEnv, "PGCONNECT_TIMEOUT=10") || !strings.Contains(sizeEnv, "statement_timeout=30000") {
				t.Error("consulta final de disco perdeu budgets próprios")
			}
		})
	}
}

func TestBackupValidatesSnapshotManifestAndExit(t *testing.T) {
	valid := "stillon-backup-snapshot-v1|1|1|0\n"
	for _, c := range []struct {
		name, stdout string
		exit, want   int
		missingDump  bool
	}{
		{name: "válido", stdout: valid},
		{name: "vazio", want: 1},
		{name: "magic errado", stdout: "outro|1|1|0\n", want: 1},
		{name: "linhas múltiplas", stdout: valid + valid, want: 1},
		{name: "campos extras", stdout: "stillon-backup-snapshot-v1|1|1|0|extra\n", want: 1},
		{name: "número inválido", stdout: "stillon-backup-snapshot-v1|1|1|-1\n", want: 1},
		{name: "sem LF", stdout: strings.TrimSuffix(valid, "\n"), want: 1},
		{name: "falha com stdout válido", stdout: valid, exit: 2, want: 1},
		{name: "SIGINT", stdout: valid, exit: 130, want: 130},
		{name: "SIGTERM", stdout: valid, exit: 143, want: 143},
		{name: "sem dump", stdout: valid, missingDump: true, want: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeScript(t, filepath.Join(dir, "psql"), "#!/bin/sh\nset -eu\nprintf 'chamada\\n' >> \"$REGISTRO/chamado\"\ncase \"$*\" in *pg_database_size*) echo 1234 ;; esac\n")
			writeScript(t, filepath.Join(dir, "pg_restore"), "#!/bin/sh\nset -eu\nprintf 'chamada\\n' >> \"$REGISTRO/chamado\"\n")
			helper := filepath.Join(dir, "snapshot-helper")
			body := "#!/bin/sh\nset -eu\n"
			if !c.missingDump {
				body += "echo publico > \"$SAIDA\"\n"
			}
			body += "printf '%s' '" + strings.ReplaceAll(c.stdout, "'", "'\"'\"'") + "'\nexit " + fmt.Sprint(c.exit) + "\n"
			writeScript(t, helper, body)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", backupScriptWithHelper(t, dir, helper))
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "ORIGEM_URL=postgres://stillon_backup:senha-publica@127.0.0.1:5432/stillon?sslmode=disable", "PGHOST=127.0.0.1", "PGPASSWORD=restauro-publico", "SAIDA="+filepath.Join(dir, "dump"))
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatalf("executar shell: %v", err)
				}
			}
			if code != c.want {
				t.Fatalf("código%d, esperado%d\n%s", code, c.want, out)
			}
			_, called := os.Stat(filepath.Join(dir, "chamado"))
			if (called == nil) != (c.want == 0) {
				t.Fatal("restauração/SQL avançou sem helper, manifesto e dump válidos")
			}
		})
	}
}

func TestBackupRefusesCredentialInQueryBeforeCallingPG(t *testing.T) {
	keys := []string{"password", "%70assword", "sslpassword", "ssl%70assword"}
	for _, key := range []string{"passfile", "options", "service", "application_name", "fallback_application_name", "connect_timeout", "oauth_client_secret", "scram_client_key", "scram_server_key"} {
		keys = append(keys, key, strings.ToUpper(key), fmt.Sprintf("%%%02x%s", key[0], key[1:]))
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			writeScript(t, filepath.Join(dir, "psql"), "#!/bin/sh\necho chamado > \"$REGISTRO/chamado\"\nexit 1\n")
			helper := filepath.Join(dir, "snapshot-helper")
			writeScript(t, helper, "#!/bin/sh\necho chamado > \"$REGISTRO/chamado\"\nexit 1\n")
			cmd := exec.Command("sh", backupScriptWithHelper(t, dir, helper))
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "REGISTRO="+dir,
				"ORIGEM_URL=postgres://stillon_backup:sentinela-autoridade@127.0.0.1:5432/stillon?"+key+"=sentinela-query",
				"PGHOST=127.0.0.1", "SAIDA="+filepath.Join(dir, "dump"))
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Error("backup aceitou credencial na query")
			}
			if _, err := os.Stat(filepath.Join(dir, "chamado")); err == nil {
				t.Error("backup chamou PG antes de recusar credencial na query")
			}
			if strings.Contains(string(out), "sentinela-") {
				t.Error("recusa imprimiu credencial")
			}
		})
	}
}

// A cópia conserva o script e o layout reais. Só o path fixo do executável
// é substituído para a fixture; não há variável de produção para trocar helper.
func backupScriptWithHelper(t *testing.T, dir, helper string) string {
	t.Helper()
	backupDir := filepath.Join(dir, "deploy", "backup")
	composeDir := filepath.Join(dir, "deploy", "compose")
	for _, path := range []string{backupDir, composeDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{"backup/backup.sh", "backup/verificar.sql", "compose/initdb.sql"} {
		body, err := os.ReadFile(filepath.Join("../../deploy", relative))
		if err != nil {
			t.Fatal(err)
		}
		if relative == "backup/backup.sh" {
			quoted := "'" + strings.ReplaceAll(helper, "'", "'\"'\"'") + "'"
			body = []byte(strings.ReplaceAll(string(body), "/stillon backup-snapshot", quoted+" backup-snapshot"))
			// O drop real é coberto pelo ensaio PG. Aqui o launcher falso
			// conserva EXEC e só observa a fronteira de argumentos.
			gosu := filepath.Join(dir, "gosu-fixture")
			writeScript(t, gosu, "#!/bin/sh\nshift\nexec \"$@\"\n")
			body = []byte(strings.ReplaceAll(string(body), "/usr/local/bin/gosu", "'"+strings.ReplaceAll(gosu, "'", "'\"'\"'")+"'"))
		}
		if err := os.WriteFile(filepath.Join(dir, "deploy", relative), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(backupDir, "backup.sh")
}

// Trocar :'app' por :app ou remover \getenv deve quebrar a criação/autenticação,
// mesmo quando o teste de argv continua verde. O banco aqui é só descartável.
func TestInitDBAppliesLiteralPasswords(t *testing.T) {
	if testing.Short() {
		t.Skip("precisa de Docker; pulado em -short")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal("teste de integração precisa do Docker")
	}
	// Montado com o modo da produção (o runbook instala 0755), e não com o 0644
	// do checkout: o bash do entrypoint pergunta -x ao VirtioFS do Docker
	// Desktop, que responde pelo dono no host, e o exec falha em seguida.
	source, err := os.ReadFile("../../deploy/vps/initdb.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "initdb.sh")
	if err := os.WriteFile(script, source, 0o755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("stillon-initdb-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	app, backup := "sentinela-app'\\fim:;--", "sentinela-backup'\\fim:;--"
	imageBytes, err := os.ReadFile("../../deploy/vps/db/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var image string
	for _, line := range strings.Split(string(imageBytes), "\n") {
		if strings.HasPrefix(line, "FROM ") {
			image = strings.TrimPrefix(line, "FROM ")
			break
		}
	}
	if image == "" {
		t.Fatal("Dockerfile do banco sem FROM")
	}
	// O pg_hba.conf da imagem dá trust a 127.0.0.1: sem --auth-host, qualquer
	// senha entraria e o teste não provaria nada sobre os literais.
	cmd := exec.CommandContext(ctx, docker, "run", "-d", "--name", name, "--network", "none",
		"-e", "POSTGRES_PASSWORD", "-e", "STILLON_APP_PASSWORD", "-e", "STILLON_BACKUP_PASSWORD",
		"-e", "POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256",
		"--mount", "type=bind,src="+script+",dst=/docker-entrypoint-initdb.d/10-stillon.sh,readonly", image)
	cmd.Env = append(os.Environ(), "POSTGRES_PASSWORD=sentinela-admin", "STILLON_APP_PASSWORD="+app, "STILLON_BACKUP_PASSWORD="+backup)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subir banco descartável: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if out, err := exec.CommandContext(cleanupCtx, docker, "rm", "-f", "-v", name).CombinedOutput(); err != nil {
			t.Errorf("limpar banco: %v\n%s", err, out)
		}
	})
	for {
		if exec.CommandContext(ctx, docker, "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres").Run() == nil {
			break
		}
		state, _ := exec.CommandContext(ctx, docker, "inspect", "-f", "{{.State.Status}}", name).Output()
		if strings.TrimSpace(string(state)) == "exited" {
			t.Fatal("inicialização do Postgres descartável falhou antes de aceitar conexões")
		}
		if ctx.Err() != nil {
			t.Fatal("Postgres descartável não ficou pronto")
		}
		time.Sleep(200 * time.Millisecond)
	}
	login := func(user, password string) (string, error) {
		cmd := exec.CommandContext(ctx, docker, "exec", "-e", "PGPASSWORD", name, "psql", "-X", "-Atq", "-h", "127.0.0.1", "-U", user, "-d", "stillon", "-c", "SELECT current_user")
		cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, credentials := range []struct{ user, password string }{{"stillon_app", app}, {"stillon_backup", backup}} {
		if out, err := login(credentials.user, credentials.password); err != nil || strings.TrimSpace(out) != credentials.user {
			t.Errorf("autenticar %s com senha literal: %v\n%s", credentials.user, err, out)
		}
		if out, err := login(credentials.user, "senha-errada"); err == nil {
			t.Errorf("%s entrou com senha errada: a autenticação não está sendo exercitada\n%s", credentials.user, out)
		}
	}
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	// env-i da produção limpa variáveis de fixture. O destino do registro
	// fica somente neste executável falso, nunca em uma opção do produto.
	quoted := "'" + strings.ReplaceAll(filepath.Dir(path), "'", "'\"'\"'") + "'"
	body = strings.Replace(body, "#!/bin/sh\n", "#!/bin/sh\nREGISTRO="+quoted+"\n", 1)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func readScriptRecord(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
