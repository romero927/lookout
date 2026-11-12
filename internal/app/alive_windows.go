//go:build windows

package app

import "os"

// processAlive reports whether a process with this ID exists. On Windows
// FindProcess opens the process, which fails when it is gone.
func processAlive(pid int, _ string) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
