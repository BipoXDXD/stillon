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

// origem é a conexão de leitura do backup: URI pública (validada, sem
// segredo) e a senha, que só viaja no ambiente dos clientes.
type origem struct{ uri, senha string }

// run supervisiona uma execução. Os defers registrados aqui formam a ordem de
// encerramento (o último registrado roda primeiro) e por isso ficam todos no
// mesmo corpo; os passos lineares vivem nas funções abaixo, na ordem em que
// run as chama.
func run(parent context.Context, publicURI, password, output string, o options) (ref Reference, err error) {
	if e := validarEntrada(publicURI, password, output); e != nil {
		return ref, e
	}
	if parent.Err() != nil {
		return ref, parent.Err()
	}
	src := origem{uri: publicURI, senha: password}
	ctx, cancel := context.WithTimeout(parent, o.totalTimeout)
	defer cancel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := supported(); e != nil {
		return ref, e
	}
	created, e := criarSaida(output)
	defer func() {
		if err != nil && created != nil {
			if descarteErr := descartarSaida(output, created); descarteErr != nil {
				err = descarteErr
			}
		}
	}()
	if e != nil {
		return ref, e
	}
	if o.protectParent {
		release, e := protectParent()
		if e != nil {
			return ref, e
		}
		defer func() {
			if releaseErr := release(); releaseErr != nil && err == nil {
				ref = Reference{}
				err = fmt.Errorf("proteção parental não foi desarmada: %w", releaseErr)
			}
		}()
	}
	identity, e := novaIdentidade()
	if e != nil {
		return ref, e
	}
	exporterCtx, cancelExporter := context.WithCancel(ctx)
	defer cancelExporter()
	exportName, dumpName := identity+"_exporter", identity+"_dump"
	p, e := abrirPipes()
	if e != nil {
		return ref, e
	}
	defer p.fecharTudo()
	closeDone := make(chan error, 1)
	stopClose := context.AfterFunc(exporterCtx, func() { closeDone <- errors.Join(closePipe(p.inWrite), closePipe(p.outRead)) })
	defer func() {
		if !stopClose() {
			if closeErr := <-closeDone; closeErr != nil && err == nil {
				ref = Reference{}
				err = fmt.Errorf("fechamento dos pipes falhou: %w", closeErr)
			}
		}
	}()
	null, e := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if e != nil {
		return ref, fmt.Errorf("saída privada indisponível: %w", e)
	}
	defer null.Close()
	exporter := novoExportador(exporterCtx, o, src, exportName, p, null)
	if e = exporter.Start(); e != nil {
		return ref, fmt.Errorf("exportador não iniciou: %w", e)
	}
	exporterWaited := false
	var dump *exec.Cmd
	dumpWaited := false
	defer func() {
		cancel()
		var pendentes []*exec.Cmd
		if dump != nil && !dumpWaited {
			pendentes = append(pendentes, dump)
		}
		if !exporterWaited {
			pendentes = append(pendentes, exporter)
		}
		// Em falha, a primeira causa já é sanitizada. Os resultados do cleanup
		// são consumidos; não podem converter a falha em sucesso.
		if problema := encerrarClientes(p, pendentes...); problema != nil && err == nil {
			ref = Reference{}
			err = fmt.Errorf("encerramento dos clientes falhou: %w", problema)
		}
	}()
	if e = errors.Join(closePipe(p.inRead), closePipe(p.outWrite)); e != nil {
		return ref, fmt.Errorf("fechamento das extremidades dos filhos falhou: %w", e)
	}
	s := &supervisao{ctx: ctx, inWrite: p.inWrite, outRead: p.outRead, reader: bufio.NewReaderSize(p.outRead, maxLinhaProtocolo)}
	snapshot, ref, e := s.prepararReferencia(o.prepareTimeout)
	if e != nil {
		return Reference{}, e
	}
	if o.afterReference != nil {
		if e = o.afterReference(ctx, ref); e != nil {
			return Reference{}, fmt.Errorf("preparação interrompida: %w", e)
		}
	}
	dumpCtx, cancelDump := context.WithTimeout(ctx, o.dumpTimeout)
	defer cancelDump()
	dump = novoDump(dumpCtx, o, src, dumpName, snapshot, output, null)
	if e = dump.Start(); e != nil {
		dump = nil
		return Reference{}, fmt.Errorf("dump não iniciou: %w", e)
	}
	e = dump.Wait()
	dumpWaited = true
	if e != nil {
		// O prazo do dump vence como sinal no processo; a causa que importa
		// para o log é o prazo, e ela vai junto.
		return Reference{}, fmt.Errorf("dump interrompido ou falhou: %w", errors.Join(e, context.Cause(dumpCtx)))
	}
	finishDone := make(chan struct{})
	finishTimer := time.AfterFunc(o.cleanup, func() { cancelExporter(); close(finishDone) })
	defer func() {
		if !finishTimer.Stop() {
			<-finishDone
		}
	}()
	if e = s.definirPrazos(o.cleanup); e != nil {
		return Reference{}, e
	}
	if e = s.aguardarSessoesEncerradas(o.cleanup, exportName, dumpName); e != nil {
		return Reference{}, e
	}
	if e = s.pedirFimDoExportador(); e != nil {
		return Reference{}, e
	}
	e = exporter.Wait()
	exporterWaited = true
	if e != nil {
		return Reference{}, fmt.Errorf("exportador não encerrou corretamente: %w", e)
	}
	if e = errors.Join(context.Cause(ctx), context.Cause(exporterCtx)); e != nil {
		return Reference{}, fmt.Errorf("prazo ou cancelamento antes da conclusão: %w", e)
	}
	if e = conferirSaida(output, created); e != nil {
		return Reference{}, e
	}
	return ref, nil
}

// encerrarClientes fecha as pontas do pai e encerra os clientes que ainda não
// foram esperados, nessa ordem e todos, mesmo depois de um problema. Close
// duplo por cancelamento é normalizado por closePipe.
func encerrarClientes(p pipes, pendentes ...*exec.Cmd) error {
	problemas := []error{closePipe(p.inWrite), closePipe(p.outRead)}
	for _, cmd := range pendentes {
		problemas = append(problemas, terminate(cmd))
		problemas = append(problemas, cmd.Wait())
	}
	return errors.Join(problemas...)
}

func validarEntrada(publicURI, password, output string) error {
	if e := validateURI(publicURI); e != nil {
		return e
	}
	if password == "" || strings.ContainsRune(password, '\x00') || output == "" || strings.ContainsRune(output, '\x00') {
		return errors.New("origem ou saída incompleta")
	}
	return nil
}

// criarSaida cria a saída desta execução. A saída pertence a esta execução;
// nunca truncar um arquivo ou symlink anterior. O chamador fornece um
// diretório privado e mantém esse domínio de escrita até o restauro (a
// reabertura por pg_dump usa o mesmo pathname). O FileInfo devolvido, mesmo
// com erro, identifica o inode criado para o descarte.
func criarSaida(output string) (os.FileInfo, error) {
	artifact, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 -- SAIDA é o contrato de arquivo privado; O_EXCL recusa arquivos/symlinks anteriores e o inode é conferido.
	if e != nil {
		return nil, fmt.Errorf("saída não é um arquivo novo gravável: %w", e)
	}
	created, e := artifact.Stat()
	if e == nil {
		e = artifact.Chmod(0600)
	}
	closeErr := artifact.Close()
	if e = errors.Join(e, closeErr); e != nil {
		return created, fmt.Errorf("saída privada indisponível: %w", e)
	}
	if created == nil || !created.Mode().IsRegular() {
		return created, errors.New("saída privada indisponível")
	}
	return created, nil
}

// descartarSaida remove a saída parcial, só se ela ainda for o inode que esta
// execução criou.
func descartarSaida(output string, created os.FileInfo) error {
	current, statErr := os.Lstat(output)
	if statErr != nil || !os.SameFile(created, current) {
		return nil
	}
	if removeErr := os.Remove(output); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("descarte da saída parcial falhou: %w", removeErr)
	}
	return nil
}

func novaIdentidade() (string, error) {
	var token [16]byte
	if _, e := rand.Read(token[:]); e != nil {
		return "", fmt.Errorf("identidade da execução indisponível: %w", e)
	}
	return "stillon_snapshot_" + hex.EncodeToString(token[:]), nil
}

// pipes são as duas conversas com o exportador: in (stdin dele) e out
// (stdout dele). As extremidades dos filhos (inRead, outWrite) fecham no pai
// logo depois do Start.
type pipes struct{ inRead, inWrite, outRead, outWrite *os.File }

func abrirPipes() (pipes, error) {
	inRead, inWrite, e := os.Pipe()
	if e != nil {
		return pipes{}, fmt.Errorf("pipe de entrada indisponível: %w", e)
	}
	outRead, outWrite, e := os.Pipe()
	if e != nil {
		_ = inWrite.Close() // a falha que importa é a do pipe de saída
		_ = inRead.Close()
		return pipes{}, fmt.Errorf("pipe de saída indisponível: %w", e)
	}
	return pipes{inRead: inRead, inWrite: inWrite, outRead: outRead, outWrite: outWrite}, nil
}

// fecharTudo é a rede final: os fechamentos que importam já foram conferidos
// por closePipe, e aqui sobra só o já fechado ou o caminho de falha.
func (p pipes) fecharTudo() {
	_ = p.outWrite.Close()
	_ = p.outRead.Close()
	_ = p.inWrite.Close()
	_ = p.inRead.Close()
}

func novoExportador(ctx context.Context, o options, src origem, nome string, p pipes, null *os.File) *exec.Cmd {
	cmd := exec.CommandContext(ctx, o.psqlPath, "-XAtq", "--no-password", "-v", "ON_ERROR_STOP=1", src.uri) // #nosec G204 -- Executável fixo no produto; testes privados trocam o path. URI validada é um argumento, sem shell/segredo.
	cmd.Env = sourceEnv(os.Environ(), src.senha, nome, opcoesDoExportador(o), o.connectTimeout)
	cmd.Stdin = p.inRead
	cmd.Stdout = p.outWrite
	configurarCliente(cmd, o, null)
	return cmd
}

func novoDump(ctx context.Context, o options, src origem, nome, snapshot, output string, null *os.File) *exec.Cmd {
	cmd := exec.CommandContext(ctx, o.dumpPath, "--format=custom", "--no-password", "--snapshot="+snapshot, "--lock-wait-timeout="+strconv.FormatInt(milissegundos(o.connectTimeout), 10), "--file="+output, src.uri) // #nosec G204 -- Path fixo no produto e argumentos individuais; snapshot validado, SAIDA nova e URI sem segredo, nunca shell.
	cmd.Env = sourceEnv(os.Environ(), src.senha, nome, "-c client_connection_check_interval="+strconv.FormatInt(milissegundos(o.checkInterval), 10), o.connectTimeout)
	cmd.Stdout = null
	configurarCliente(cmd, o, null)
	return cmd
}

func configurarCliente(cmd *exec.Cmd, o options, null *os.File) {
	protectChild(cmd)
	cmd.Cancel = func() error { return terminate(cmd) }
	cmd.WaitDelay = o.grace
	cmd.Stderr = null
}

func opcoesDoExportador(o options) string {
	return fmt.Sprintf("-c transaction_timeout=%d -c idle_in_transaction_session_timeout=%d -c statement_timeout=%d -c lock_timeout=%d -c client_connection_check_interval=%d", milissegundos(o.totalTimeout), milissegundos(o.totalTimeout), milissegundos(o.prepareTimeout), milissegundos(o.connectTimeout), milissegundos(o.checkInterval))
}

// milissegundos arredonda para baixo, com piso de 1: zero desligaria o prazo
// no Postgres.
func milissegundos(d time.Duration) int64 {
	return max(d.Milliseconds(), 1)
}

// maxLinhaProtocolo é o maior tamanho de linha que o exportador pode mandar.
const maxLinhaProtocolo = 512

const consultaReferencia = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSELECT pg_export_snapshot(),(SELECT max(version_id) FROM goose_db_version),(SELECT count(*) FROM users),(SELECT count(*) FROM targets);\n"

// supervisao é a conversa com o exportador pelo protocolo de linhas do psql.
type supervisao struct {
	ctx     context.Context
	inWrite *os.File
	outRead *os.File
	reader  *bufio.Reader
}

func (s *supervisao) definirPrazos(d time.Duration) error {
	limit := time.Now().Add(d)
	if deadline, ok := s.ctx.Deadline(); ok && deadline.Before(limit) {
		limit = deadline
	}
	if e := s.inWrite.SetWriteDeadline(limit); e != nil {
		return fmt.Errorf("deadline da entrada indisponível: %w", e)
	}
	if e := s.outRead.SetReadDeadline(limit); e != nil {
		return fmt.Errorf("deadline da saída indisponível: %w", e)
	}
	return nil
}

func (s *supervisao) lerLinha() (string, error) {
	data, e := s.reader.ReadSlice('\n')
	if e != nil {
		return "", fmt.Errorf("protocolo da origem interrompido ou excessivo: %w", e)
	}
	line := string(data)
	if len(line) > maxLinhaProtocolo {
		return "", errors.New("protocolo da origem interrompido ou excessivo")
	}
	return line, nil
}

// prepararReferencia abre a transação RR, exporta o snapshot e lê a
// referência que o restauro vai conferir.
func (s *supervisao) prepararReferencia(prazo time.Duration) (string, Reference, error) {
	if e := s.definirPrazos(prazo); e != nil {
		return "", Reference{}, e
	}
	if _, e := io.WriteString(s.inWrite, consultaReferencia); e != nil {
		return "", Reference{}, fmt.Errorf("preparação da origem interrompida: %w", e)
	}
	line, e := s.lerLinha()
	if e != nil {
		return "", Reference{}, e
	}
	return parseReference(line)
}

// aguardarSessoesEncerradas espera, até prazo, que nenhuma outra sessão desta
// execução continue aberta na origem.
func (s *supervisao) aguardarSessoesEncerradas(prazo time.Duration, exportName, dumpName string) error {
	// pg_stat_clear_snapshot descarta qualquer cache de estatísticas da transação
	// RR. As identidades são geradas internamente em hexadecimal, nunca da URI.
	clearAndCount := fmt.Sprintf("SELECT pg_stat_clear_snapshot();\nSELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename=current_user AND application_name IN ('%s','%s') AND pid<>pg_backend_pid();\n", exportName, dumpName)
	end := time.Now().Add(prazo)
	for {
		n, e := s.contarSessoes(clearAndCount)
		if e != nil {
			return e
		}
		if n == 0 {
			return nil
		}
		if !time.Now().Before(end) {
			return errors.New("sessão da execução permaneceu aberta")
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return fmt.Errorf("checagem das sessões interrompida: %w", context.Cause(s.ctx))
		case <-timer.C:
		}
	}
}

func (s *supervisao) contarSessoes(consulta string) (uint64, error) {
	if _, e := io.WriteString(s.inWrite, consulta); e != nil {
		return 0, fmt.Errorf("checagem das sessões interrompida: %w", e)
	}
	blank, e := s.lerLinha()
	if e != nil {
		return 0, fmt.Errorf("checagem das sessões inválida: %w", e)
	}
	if blank != "\n" {
		return 0, errors.New("checagem das sessões inválida")
	}
	count, e := s.lerLinha()
	if e != nil {
		return 0, e
	}
	n, e := strconv.ParseUint(strings.TrimSuffix(count, "\n"), 10, 64)
	if e != nil {
		// Sem %w: o erro do ParseUint repete a linha lida da origem, e nada
		// que a origem mande entra numa mensagem.
		return 0, errors.New("checagem das sessões inválida")
	}
	return n, nil
}

// pedirFimDoExportador encerra a transação e a entrada do psql, e exige que
// nada mais saia dele.
func (s *supervisao) pedirFimDoExportador() error {
	if _, e := io.WriteString(s.inWrite, "ROLLBACK;\n\\q\n"); e != nil {
		return fmt.Errorf("fechamento do exportador interrompido: %w", e)
	}
	if e := closePipe(s.inWrite); e != nil {
		return fmt.Errorf("fechamento da entrada do exportador falhou: %w", e)
	}
	remainder, e := io.ReadAll(io.LimitReader(s.reader, maxLinhaProtocolo+1))
	if e != nil {
		return fmt.Errorf("fechamento do exportador inválido: %w", e)
	}
	if len(remainder) != 0 {
		return errors.New("fechamento do exportador inválido")
	}
	return nil
}

// conferirSaida exige que a saída ainda seja o inode privado e regular que
// esta execução criou.
func conferirSaida(output string, created os.FileInfo) error {
	current, statErr := os.Lstat(output)
	if statErr != nil {
		return fmt.Errorf("saída da execução foi substituída ou alterada: %w", statErr)
	}
	if !os.SameFile(created, current) || !current.Mode().IsRegular() || current.Mode().Perm() != 0600 {
		return errors.New("saída da execução foi substituída ou alterada")
	}
	return nil
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
