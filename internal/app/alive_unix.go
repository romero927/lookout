//go:build !windows

package app

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"syscall"
)

// processAlive reports whether the process with this ID that started at
// procStart (Claude Code's "procStart", "" when not given) still runs.
// Where /proc tells start times (Linux), a PID reused by another process
// doesn't count.
func processAlive(pid int, procStart string) bool {
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if got, ok := procStartOf(pid); ok && procStart != "" {
		return got == procStart
	}
	return true
}

// procStartOf reads a process's start time (field 22 of /proc/<pid>/stat,
// in clock ticks since boot).
func procStartOf(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	// Field 2, the command name, is in parentheses and may hold spaces.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return "", false
	}
	f := bytes.Fields(b[i+1:]) // starts at field 3
	if len(f) < 20 {
		return "", false
	}
	return string(f[19]), true
}
