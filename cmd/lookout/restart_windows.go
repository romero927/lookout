//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
)

// restartSelf runs the binary at exe again and exits with its status.
func restartSelf(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	if err == nil {
		os.Exit(0)
	}
	return err
}
