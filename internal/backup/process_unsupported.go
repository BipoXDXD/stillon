//go:build !linux

package backup

import (
	"errors"
	"os/exec"
)

func supported() error                     { return errors.New("backup supervisionado exige Linux") }
func protectChild(*exec.Cmd)               {}
func terminate(*exec.Cmd) error            { return supported() }
func protectParent() (func() error, error) { return nil, supported() }
