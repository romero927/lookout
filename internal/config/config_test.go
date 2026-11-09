package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content *string
		check   func(t *testing.T, c Config)
		wantErr bool
	}{
		{name: "missing file gives defaults", content: nil, check: func(t *testing.T, c Config) {
			if !reflect.DeepEqual(c, Default()) {
				t.Fatalf("got %+v", c)
			}
		}},
		{name: "partial override keeps other defaults", content: ptr("[models]\nrepeated_edit_threshold = 7\n"), check: func(t *testing.T, c Config) {
			if c.Models.RepeatedEditThreshold != 7 {
				t.Fatalf("threshold = %d", c.Models.RepeatedEditThreshold)
			}
			if c.Models.ToolErrorThreshold != Default().Models.ToolErrorThreshold {
				t.Fatal("default lost")
			}
		}},
		{name: "zero values fall back", content: ptr("[usage]\nblock_hours = 0\n"), check: func(t *testing.T, c Config) {
			if c.Usage.BlockHours != 5 {
				t.Fatalf("block hours = %d", c.Usage.BlockHours)
			}
		}},
		{name: "files edit opt-in", content: ptr("[files]\nallow_edit = true\n"), check: func(t *testing.T, c Config) {
			if !c.Files.AllowEdit {
				t.Fatal("allow_edit not set")
			}
		}},
		{name: "invalid toml", content: ptr("[[[nope"), wantErr: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, "c", string(rune('a'+i))+".toml")
			if tt.content != nil {
				_ = os.MkdirAll(filepath.Dir(p), 0o755)
				if err := os.WriteFile(p, []byte(*tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c, err := Load(p)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}

// The commented default file must decode to exactly Default().
func TestDefaultTOMLMatchesDefault(t *testing.T) {
	var c Config
	if _, err := toml.Decode(DefaultTOML, &c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Fatalf("DefaultTOML drifted from Default():\n%+v\n%+v", c, Default())
	}
}

func ptr(s string) *string { return &s }
