package main

import (
	"context"
	"fmt"
	"github.com/BipoXDXD/stillon/internal/backup"
	"io"
)

func runBackupSnapshot(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "uso: stillon backup-snapshot (origem e saída vêm do ambiente)")
		return saidaUso
	}
	ref, err := backup.Run(ctx, getenv("STILLON_ORIGEM_PUBLICA"), getenv("PGPASSWORD"), getenv("SAIDA"))
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "backup-snapshot: interrompido")
			return saidaInterrompida
		}
		fmt.Fprintln(stderr, "backup-snapshot:", err)
		return 1
	}
	if _, err = fmt.Fprintf(stdout, "%s|%d|%d|%d\n", backup.Protocol, ref.Version, ref.Users, ref.Targets); err != nil {
		fmt.Fprintln(stderr, "backup-snapshot: publicação do protocolo falhou")
		return 1
	}
	return 0
}
