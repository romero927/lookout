package app

import (
	"os"
	"testing"

	"github.com/romero927/lookout/internal/config"
)

// BenchmarkLoad scans the real ~/.claude/projects when LOOKOUT_BENCH_LOAD=1.
func BenchmarkLoad(b *testing.B) {
	if os.Getenv("LOOKOUT_BENCH_LOAD") == "" {
		b.Skip("set LOOKOUT_BENCH_LOAD=1 to scan the local transcripts")
	}
	for i := 0; i < b.N; i++ {
		New(config.Default(), "", nil).Load()
	}
}
