//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restartSelf replaces this process with the binary at exe, same arguments.
func restartSelf(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
