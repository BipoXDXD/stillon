// Package backup supervisiona a referência consistente e o dump; o restauro
// e a aceitação do backup continuam no chamador.
package backup

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const Protocol = "stillon-backup-snapshot-v1"

type Reference struct{ Version, Users, Targets int64 }

type options struct {
	connectTimeout, prepareTimeout, dumpTimeout, totalTimeout time.Duration
	grace, cleanup, checkInterval                             time.Duration
	psqlPath, dumpPath                                        string
	protectParent                                             bool
	afterReference                                            func(context.Context, Reference) error
}

func defaultOptions() options {
	return options{connectTimeout: 10 * time.Second, prepareTimeout: 30 * time.Second, dumpTimeout: 540 * time.Second, totalTimeout: 600 * time.Second, grace: 5 * time.Second, cleanup: 5 * time.Second, checkInterval: time.Second, psqlPath: "/usr/local/bin/psql", dumpPath: "/usr/local/bin/pg_dump", protectParent: true}
}

// Run não imprime nem inclui URI, senha ou stderr libpq nos erros. O sucesso
// exige dump/Wait, ausência de outras sessões da execução e rollback/EOF/Wait
// do exportador. A ausência posterior do próprio backend é verificada nos
// ensaios por uma conexão observadora externa.
func Run(ctx context.Context, publicURI, password, output string) (Reference, error) {
	return run(ctx, publicURI, password, output, defaultOptions())
}

func validateURI(raw string) error {
	invalid := errors.New("URI pública da origem inválida ou com parâmetro reservado")
	if strings.ContainsAny(raw, "\x00\r\n") {
		return invalid
	}
	u, e := url.Parse(raw)
	if e != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Fragment != "" {
		return invalid
	}
	if _, has := u.User.Password(); has {
		return invalid
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return invalid
	}
	for key := range q {
		switch strings.ToLower(key) {
		case "options", "service", "application_name", "fallback_application_name", "connect_timeout", "password", "sslpassword", "passfile", "oauth_client_secret", "scram_client_key", "scram_server_key":
			return invalid
		}
	}
	return nil
}

var snapshotID = regexp.MustCompile(`^[0-9A-Fa-f]+-[0-9A-Fa-f]+-[0-9]+$`)

func parseReference(line string) (string, Reference, error) {
	bad := errors.New("referência da origem inválida")
	if len(line) > 512 || !strings.HasSuffix(line, "\n") {
		return "", Reference{}, bad
	}
	fields := strings.Split(strings.TrimSuffix(line, "\n"), "|")
	if len(fields) != 4 || !snapshotID.MatchString(fields[0]) {
		return "", Reference{}, bad
	}
	nums := [3]int64{}
	for i, field := range fields[1:] {
		if field == "" || strings.ContainsAny(field, "+- \r\n\t") {
			return "", Reference{}, bad
		}
		n, e := strconv.ParseInt(field, 10, 64)
		if e != nil || n < 0 {
			return "", Reference{}, bad
		}
		nums[i] = n
	}
	return fields[0], Reference{Version: nums[0], Users: nums[1], Targets: nums[2]}, nil
}

func sourceEnv(inherited []string, password, app, pgOptions string, connect time.Duration) []string {
	env := make([]string, 0, len(inherited)+4)
	for _, v := range inherited {
		key, _, _ := strings.Cut(v, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "PG") {
			env = append(env, v)
		}
	}
	seconds := int64((connect + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return append(env, "PGPASSWORD="+password, "PGAPPNAME="+app, "PGOPTIONS="+pgOptions, "PGCONNECT_TIMEOUT="+strconv.FormatInt(seconds, 10))
}

func run(parent context.Context, publicURI, password, output string, o options) (ref Reference, err error) {
	if e := validateURI(publicURI); e != nil {
		return ref, e
	}
	if password == "" || strings.ContainsRune(password, '\x00') || output == "" || strings.ContainsRune(output, '\x00') {
		return ref, errors.New("origem ou saída incompleta")
	}
	if parent.Err() != nil {
		return ref, parent.Err()
	}
	ctx, cancel := context.WithTimeout(parent, o.totalTimeout)
	defer cancel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := supported(); e != nil {
		return ref, e
	}
	// A saída pertence a esta execução; nunca truncar um arquivo ou symlink
	// anterior. O chamador fornece um diretório privado e mantém esse domínio
	// de escrita até o restauro (a reabertura por pg_dump usa o mesmo pathname).
	artifact, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 -- SAIDA é o contrato de arquivo privado; O_EXCL recusa arquivos/symlinks anteriores e o inode é conferido.
	if e != nil {
		return ref, errors.New("saída não é um arquivo novo gravável")
	}
	created, e := artifact.Stat()
	if e == nil {
		e = artifact.Chmod(0600)
	}
	closeErr := artifact.Close()
	defer func() {
		if err != nil && created != nil {
			if current, statErr := os.Lstat(output); statErr == nil && os.SameFile(created, current) {
				if removeErr := os.Remove(output); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = errors.New("descarte da saída parcial falhou")
				}
			}
		}
	}()
	if e != nil || closeErr != nil || created == nil || !created.Mode().IsRegular() {
		return ref, errors.New("saída privada indisponível")
	}
	if o.protectParent {
		release, e := protectParent()
		if e != nil {
			return ref, e
		}
		defer func() {
			if releaseErr := release(); releaseErr != nil && err == nil {
				ref = Reference{}
				err = errors.New("proteção parental não foi desarmada")
			}
		}()
	}
	var token [16]byte
	if _, e := rand.Read(token[:]); e != nil {
		return ref, errors.New("identidade da execução indisponível")
	}
	exporterCtx, cancelExporter := context.WithCancel(ctx)
	defer cancelExporter()
	identity := "stillon_snapshot_" + hex.EncodeToString(token[:])
	exportName, dumpName := identity+"_exporter", identity+"_dump"
	inRead, inWrite, e := os.Pipe()
	if e != nil {
		return ref, errors.New("pipe de entrada indisponível")
	}
	defer inRead.Close()
	defer inWrite.Close()
	outRead, outWrite, e := os.Pipe()
	if e != nil {
		return ref, errors.New("pipe de saída indisponível")
	}
	defer outRead.Close()
	defer outWrite.Close()
	closeDone := make(chan error, 1)
	stopClose := context.AfterFunc(exporterCtx, func() { closeDone <- errors.Join(closePipe(inWrite), closePipe(outRead)) })
	defer func() {
		if !stopClose() {
			if closeErr := <-closeDone; closeErr != nil && err == nil {
				ref = Reference{}
				err = errors.New("fechamento dos pipes falhou")
			}
		}
	}()
	null, e := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if e != nil {
		return ref, errors.New("saída privada indisponível")
	}
	defer null.Close()
	milliseconds := func(d time.Duration) int64 {
		n := d.Milliseconds()
		if n < 1 {
			n = 1
		}
		return n
	}
	exporterOptions := fmt.Sprintf("-c transaction_timeout=%d -c idle_in_transaction_session_timeout=%d -c statement_timeout=%d -c lock_timeout=%d -c client_connection_check_interval=%d", milliseconds(o.totalTimeout), milliseconds(o.totalTimeout), milliseconds(o.prepareTimeout), milliseconds(o.connectTimeout), milliseconds(o.checkInterval))
	configure := func(cmd *exec.Cmd) {
		protectChild(cmd)
		cmd.Cancel = func() error { return terminate(cmd) }
		cmd.WaitDelay = o.grace
		cmd.Stderr = null
	}
	exporter := exec.CommandContext(exporterCtx, o.psqlPath, "-XAtq", "--no-password", "-v", "ON_ERROR_STOP=1", publicURI) // #nosec G204 -- Executável fixo no produto; testes privados trocam o path. URI validada é um argumento, sem shell/segredo.
	exporter.Env = sourceEnv(os.Environ(), password, exportName, exporterOptions, o.connectTimeout)
	exporter.Stdin = inRead
	exporter.Stdout = outWrite
	configure(exporter)
	if e = exporter.Start(); e != nil {
		return ref, errors.New("exportador não iniciou")
	}
	exporterWaited := false
	var dump *exec.Cmd
	dumpWaited := false
	defer func() {
		cancel()
		// Em falha, a primeira causa já é sanitizada. Os resultados do cleanup
		// são consumidos; não podem converter a falha em sucesso. Close duplo
		// por cancelamento é normalizado por closePipe.
		remember := func(problem error) {
			if problem != nil && err == nil {
				ref = Reference{}
				err = errors.New("encerramento dos clientes falhou")
			}
		}
		remember(closePipe(inWrite))
		remember(closePipe(outRead))
		if dump != nil && !dumpWaited {
			remember(terminate(dump))
			remember(dump.Wait())
		}
		if !exporterWaited {
			remember(terminate(exporter))
			remember(exporter.Wait())
		}
	}()
	if errors.Join(closePipe(inRead), closePipe(outWrite)) != nil {
		return ref, errors.New("fechamento das extremidades dos filhos falhou")
	}
	setDeadlines := func(d time.Duration) error {
		limit := time.Now().Add(d)
		if deadline, ok := ctx.Deadline(); ok && deadline.Before(limit) {
			limit = deadline
		}
		if e := inWrite.SetWriteDeadline(limit); e != nil {
			return errors.New("deadline da entrada indisponível")
		}
		if e := outRead.SetReadDeadline(limit); e != nil {
			return errors.New("deadline da saída indisponível")
		}
		return nil
	}
	if e = setDeadlines(o.prepareTimeout); e != nil {
		return ref, e
	}
	query := "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSELECT pg_export_snapshot(),(SELECT max(version_id) FROM goose_db_version),(SELECT count(*) FROM users),(SELECT count(*) FROM targets);\n"
	if _, e = io.WriteString(inWrite, query); e != nil {
		return ref, errors.New("preparação da origem interrompida")
	}
	reader := bufio.NewReaderSize(outRead, 512)
	readLine := func() (string, error) {
		data, e := reader.ReadSlice('\n')
		line := string(data)
		if e != nil || len(line) > 512 {
			return "", errors.New("protocolo da origem interrompido ou excessivo")
		}
		return line, nil
	}
	line, e := readLine()
	if e != nil {
		return ref, e
	}
	snapshot, ref, e := parseReference(line)
	if e != nil {
		return Reference{}, e
	}
	if o.afterReference != nil {
		if e = o.afterReference(ctx, ref); e != nil {
			return Reference{}, errors.New("preparação interrompida")
		}
	}
	dumpCtx, cancelDump := context.WithTimeout(ctx, o.dumpTimeout)
	defer cancelDump()
	dump = exec.CommandContext(dumpCtx, o.dumpPath, "--format=custom", "--no-password", "--snapshot="+snapshot, "--lock-wait-timeout="+strconv.FormatInt(milliseconds(o.connectTimeout), 10), "--file="+output, publicURI) // #nosec G204 -- Path fixo no produto e argumentos individuais; snapshot validado, SAIDA nova e URI sem segredo, nunca shell.
	dump.Env = sourceEnv(os.Environ(), password, dumpName, "-c client_connection_check_interval="+strconv.FormatInt(milliseconds(o.checkInterval), 10), o.connectTimeout)
	dump.Stdout = null
	configure(dump)
	if e = dump.Start(); e != nil {
		dump = nil
		return Reference{}, errors.New("dump não iniciou")
	}
	e = dump.Wait()
	dumpWaited = true
	if e != nil {
		return Reference{}, errors.New("dump interrompido ou falhou")
	}
	finishDone := make(chan struct{})
	finishTimer := time.AfterFunc(o.cleanup, func() { cancelExporter(); close(finishDone) })
	defer func() {
		if !finishTimer.Stop() {
			<-finishDone
		}
	}()
	if e = setDeadlines(o.cleanup); e != nil {
		return Reference{}, e
	}
	// pg_stat_clear_snapshot descarta qualquer cache de estatísticas da transação
	// RR. As identidades são geradas internamente em hexadecimal, nunca da URI.
	clearAndCount := fmt.Sprintf("SELECT pg_stat_clear_snapshot();\nSELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename=current_user AND application_name IN ('%s','%s') AND pid<>pg_backend_pid();\n", exportName, dumpName)
	end := time.Now().Add(o.cleanup)
	for {
		if _, e = io.WriteString(inWrite, clearAndCount); e != nil {
			return Reference{}, errors.New("checagem das sessões interrompida")
		}
		blank, e := readLine()
		if e != nil || blank != "\n" {
			return Reference{}, errors.New("checagem das sessões inválida")
		}
		count, e := readLine()
		if e != nil {
			return Reference{}, e
		}
		n, e := strconv.ParseUint(strings.TrimSuffix(count, "\n"), 10, 64)
		if e != nil {
			return Reference{}, errors.New("checagem das sessões inválida")
		}
		if n == 0 {
			break
		}
		if !time.Now().Before(end) {
			return Reference{}, errors.New("sessão da execução permaneceu aberta")
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Reference{}, errors.New("checagem das sessões interrompida")
		case <-timer.C:
		}
	}
	if _, e = io.WriteString(inWrite, "ROLLBACK;\n\\q\n"); e != nil {
		return Reference{}, errors.New("fechamento do exportador interrompido")
	}
	if e = closePipe(inWrite); e != nil {
		return Reference{}, errors.New("fechamento da entrada do exportador falhou")
	}
	remainder, e := io.ReadAll(io.LimitReader(reader, 513))
	if e != nil || len(remainder) != 0 {
		return Reference{}, errors.New("fechamento do exportador inválido")
	}
	e = exporter.Wait()
	exporterWaited = true
	if e != nil {
		return Reference{}, errors.New("exportador não encerrou corretamente")
	}
	if ctx.Err() != nil || exporterCtx.Err() != nil {
		return Reference{}, errors.New("prazo ou cancelamento antes da conclusão")
	}
	current, statErr := os.Lstat(output)
	if statErr != nil || !os.SameFile(created, current) || !current.Mode().IsRegular() || current.Mode().Perm() != 0600 {
		return Reference{}, errors.New("saída da execução foi substituída ou alterada")
	}
	return ref, nil
}

// O callback de cancelamento e o cleanup podem fechar o mesmo FD. Já fechado
// é benigno; os demais erros são tratados pelo chamador.
func closePipe(f *os.File) error {
	err := f.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
