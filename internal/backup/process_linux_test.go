//go:build linux

package backup

import (
	"syscall"
	"testing"
)

func TestParentDeathDuringArmingFailsSynchronously(t *testing.T) {
	reads := 0
	var signals []syscall.Signal
	release, err := protectParentWith(func() int {
		reads++
		if reads == 1 {
			return 42
		}
		return 1
	}, func(s syscall.Signal) error { signals = append(signals, s); return nil })
	if err == nil || release != nil {
		t.Fatal("pai mudou durante armamento, mas o chamador foi autorizado a iniciar clientes")
	}
	if len(signals) != 2 || signals[0] != syscall.SIGTERM || signals[1] != 0 {
		t.Fatalf("proteção não foi desarmada: %v", signals)
	}
}
