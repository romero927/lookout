package transcript

import (
	"os"
	"testing"
)

// BenchmarkReadFile parses a real transcript named by OVERWATCH_BENCH_FILE.
// It is skipped by default; no transcript is stored in the repo.
func BenchmarkReadFile(b *testing.B) {
	p := os.Getenv("OVERWATCH_BENCH_FILE")
	if p == "" {
		b.Skip("set OVERWATCH_BENCH_FILE to a transcript path")
	}
	fi, err := os.Stat(p)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(fi.Size())
	for i := 0; i < b.N; i++ {
		t := NewTailer()
		if _, err := t.Read(p, Source{}, func(Record, Source) {}); err != nil {
			b.Fatal(err)
		}
	}
}
