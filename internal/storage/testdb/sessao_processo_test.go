package testdb

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

type fixtureDoFilho struct {
	ID, Sessao, LabelSessao string
}

// O mesmo executável é iniciado duas vezes pelo mesmo pai e recebe a mesma
// configuração herdada. São Postgres reais, com os snapshots e cleanup de New.
func TestSessoesDosProcessosIsolamCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("precisa de Docker e dois processos de fixture")
	}
	ctx, cancelar := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelar()
	dir := t.TempDir()
	executavel, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	herdada := "stillon-regressao-" + rand.Text()
	iniciarFilho := func(nome string) func() {
		filhoCtx, cancelarFilho := context.WithCancel(ctx)
		cmd := exec.CommandContext(filhoCtx, executavel, "-test.run=^TestFixtureProcessoFilho$", "-test.v")
		cmd.Env = append(os.Environ(), "STILLON_FIXTURE_FILHO="+nome, "STILLON_FIXTURE_DIR="+dir, "TESTCONTAINERS_SESSION_ID="+herdada)
		log, err := os.Create(filepath.Join(dir, nome+".log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			log.Close()
			cancelarFilho()
			t.Fatal(err)
		}
		feito := make(chan struct{})
		var resultado error
		go func() {
			resultado = cmd.Wait()
			log.Close()
			close(feito)
		}()
		t.Cleanup(func() {
			cancelarFilho()
			<-feito
			if t.Failed() {
				texto, _ := os.ReadFile(filepath.Join(dir, nome+".log"))
				t.Logf("filho %s: %s", nome, texto)
			}
		})
		return func() {
			select {
			case <-feito:
				if resultado != nil {
					t.Fatalf("filho %s: %v", nome, resultado)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	lerFixture := func(nome string) fixtureDoFilho {
		p := filepath.Join(dir, nome+".json")
		aguardarArquivoDaFixture(t, ctx, p)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var f fixtureDoFilho
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	esperarA := iniciarFilho("a")
	a := lerFixture("a")
	esperarB := iniciarFilho("b")
	b := lerFixture("b")
	if a.Sessao == "" || b.Sessao == "" || a.Sessao == b.Sessao {
		t.Fatalf("processos irmãos devem ter sessões distintas: A=%q B=%q", a.Sessao, b.Sessao)
	}
	if a.LabelSessao != a.Sessao || b.LabelSessao != b.Sessao {
		t.Fatalf("sessões dos recursos divergem das sessões dos provedores: A=%+v B=%+v", a, b)
	}
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	for _, id := range []string{a.ID, b.ID} {
		if _, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); err != nil {
			t.Fatalf("fixture pronta %s: %v", id, err)
		}
	}
	gravarSinalDaFixture(t, filepath.Join(dir, "a.sair"))
	esperarA()
	// Observar a remoção real de A e do seu Ryuk, sem dormir um prazo presumido.
	for _, id := range []string{a.ID, "reaper_" + a.Sessao} {
		for {
			_, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
			if errdefs.IsNotFound(err) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("Ryuk não removeu o recurso próprio %s: %v", id, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	gravarSinalDaFixture(t, filepath.Join(dir, "b.consultar"))
	aguardarArquivoDaFixture(t, ctx, filepath.Join(dir, "b.sql-ok"))
	res, err := cli.ContainerInspect(ctx, b.ID, client.ContainerInspectOptions{})
	if err != nil || !res.Container.State.Running {
		t.Fatalf("B deve continuar vivo depois da limpeza de A: %v", err)
	}
	gravarSinalDaFixture(t, filepath.Join(dir, "b.sair"))
	esperarB()
}

// Executado somente nos filhos. O marcador de SQL é escrito apenas depois de
// uma consulta real, solicitada pelo pai após observar o cleanup do irmão.
func TestFixtureProcessoFilho(t *testing.T) {
	nome := os.Getenv("STILLON_FIXTURE_FILHO")
	if nome == "" {
		t.Skip("helper de subprocesso")
	}
	ctx, cancelar := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelar()
	dir := os.Getenv("STILLON_FIXTURE_DIR")
	pool := New(t)
	inspecao, err := container.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := fixtureDoFilho{container.GetContainerID(), testcontainers.SessionID(), inspecao.Config.Labels["org.testcontainers.sessionId"]}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, nome+".json")
	if err := os.WriteFile(p+".tmp", b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		t.Fatal(err)
	}
	if nome == "b" {
		aguardarArquivoDaFixture(t, ctx, filepath.Join(dir, "b.consultar"))
		var resposta int
		if err := pool.QueryRow(ctx, "SELECT 1").Scan(&resposta); err != nil {
			t.Fatal(err)
		}
		if resposta != 1 {
			t.Fatalf("SELECT 1 devolveu %d", resposta)
		}
		gravarSinalDaFixture(t, filepath.Join(dir, "b.sql-ok"))
	}
	aguardarArquivoDaFixture(t, ctx, filepath.Join(dir, nome+".sair"))
	fmt.Printf("fixture %s encerra depois dos sinais do pai\n", nome)
}

func aguardarArquivoDaFixture(t *testing.T, ctx context.Context, p string) {
	t.Helper()
	for {
		if _, err := os.Stat(p); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("esperar %s: %v", filepath.Base(p), ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func gravarSinalDaFixture(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("pronto\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
