package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMigrateLegacy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses XDG directories")
	}
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("LOOKOUT_DATA_DIR", "")
	t.Setenv("LOOKOUT_CONFIG_DIR", "")

	old := filepath.Join(base, "data", "overwatch")
	if err := os.MkdirAll(filepath.Join(old, "limits"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"overwatch.db", "overwatch.db-wal", "limits/s1.json"} {
		if err := os.WriteFile(filepath.Join(old, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"lookout.db", "lookout.db-wal", "limits/s1.json"} {
		if _, err := os.Stat(filepath.Join(DataDir(), f)); err != nil {
			t.Errorf("missing %s after migration: %v", f, err)
		}
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old data dir still exists")
	}

	// A second run, or a run with both present, changes nothing.
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("old dir should be left alone when the new one exists")
	}
}
