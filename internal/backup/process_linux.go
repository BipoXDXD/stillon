//go:build linux

package backup

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func supported() error              { return nil }
func protectChild(cmd *exec.Cmd)    { cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
func terminate(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }

// A goroutine chamadora permanece LockOSThread até todos os Wait. Armar a
// proteção depois do NotifyContext permite cancelamento cooperativo do helper.
func protectParent() (func() error, error) {
	return protectParentWith(os.Getppid, func(signal syscall.Signal) error {
		_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, syscall.PR_SET_PDEATHSIG, uintptr(signal), 0, 0, 0, 0)
		if errno != 0 {
			return errors.New("proteção parental indisponível")
		}
		return nil
	})
}

func protectParentWith(getParent func() int, setSignal func(syscall.Signal) error) (func() error, error) {
	parent := getParent()
	if parent == 1 {
		return nil, errors.New("pai direto indisponível")
	}
	if err := setSignal(syscall.SIGTERM); err != nil {
		return nil, err
	}
	release := func() error { return setSignal(0) }
	if getParent() != parent {
		if err := release(); err != nil {
			return nil, errors.New("pai mudou e proteção parental não foi desarmada")
		}
		return nil, errors.New("pai direto mudou durante a preparação")
	}
	return release, nil
}
