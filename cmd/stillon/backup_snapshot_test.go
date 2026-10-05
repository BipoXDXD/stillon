package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionBackupProtocolProbe(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runVersion([]string{"--backup-snapshot-protocol"}, &out, &errb); code != 0 || out.String() != "stillon-backup-snapshot-v1\n" || errb.Len() != 0 {
		t.Fatalf("probe: código%d stdout%q stderr%q", code, out.String(), errb.String())
	}
	for _, args := range [][]string{{"--backup-snapshot-protocol", "extra"}, {"--qualquer"}} {
		out.Reset()
		if code := runVersion(args, &out, &errb); code != saidaUso || out.Len() != 0 {
			t.Fatalf("flags desconhecidas: código%d stdout%q", code, out.String())
		}
	}
}

func TestBackupSnapshotCLIRefusesArgsBeforeEnvironment(t *testing.T) {
	var out, errb bytes.Buffer
	code := runBackupSnapshot(context.Background(), []string{"--timeout=0"}, &out, &errb, func(string) string { t.Fatal("argumento inválido leu ambiente"); return "" })
	if code != saidaUso || out.Len() != 0 {
		t.Fatalf("argumentos: código%d stdout%q", code, out.String())
	}
}

func TestBackupSnapshotCLIFailsWithoutProtocolOrSecrets(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), canceledBackupContext()} {
		var out, errb bytes.Buffer
		env := map[string]string{"STILLON_ORIGEM_PUBLICA": "postgresql://backup:SENTINELA@localhost/stillon", "PGPASSWORD": "SENTINELA", "SAIDA": "/tmp/dump"}
		code := runBackupSnapshot(ctx, nil, &out, &errb, func(k string) string { return env[k] })
		want := 1
		if ctx.Err() != nil {
			want = saidaInterrompida
		}
		if code != want || out.Len() != 0 || strings.Contains(errb.String(), "SENTINELA") || strings.Contains(errb.String(), "postgresql://") {
			t.Fatalf("falha: código%d stdout%q stderr%q", code, out.String(), errb.String())
		}
	}
	if _, ok := subcomandos["backup-snapshot"]; !ok {
		t.Fatal("CLI fora do despacho")
	}
}
func canceledBackupContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
