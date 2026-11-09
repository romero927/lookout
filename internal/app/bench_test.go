package app

import (
	"os"
	"testing"

	"github.com/romero927/overwatch/internal/config"
)

// BenchmarkLoad scans the real ~/.claude/projects when OVERWATCH_BENCH_LOAD=1.
func BenchmarkLoad(b *testing.B) {
	if os.Getenv("OVERWATCH_BENCH_LOAD") == "" {
		b.Skip("set OVERWATCH_BENCH_LOAD=1 to scan the local transcripts")
	}
	for i := 0; i < b.N; i++ {
		New(config.Default(), "", nil).Load()
	}
}
