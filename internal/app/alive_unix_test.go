//go:build !windows

package app

import (
	"os"
	"testing"
)

func TestProcessAliveChecksStartTime(t *testing.T) {
	pid := os.Getpid()
	start, ok := procStartOf(pid)
	if !ok {
		t.Skip("no /proc here")
	}
	if !processAlive(pid, start) || !processAlive(pid, "") {
		t.Fatal("this process should be alive")
	}
	if processAlive(pid, start+"0") {
		t.Fatal("a reused PID (different start time) should not count as alive")
	}
}
