package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		{name: "price weights override", content: ptr("[usage.price_weights]\n\"opus\" = { input = 1, output = 2, cache_write = 3, cache_write_1h = 5, cache_read = 4 }\n\"bad\" = { input = -1 }\n"), check: func(t *testing.T, c Config) {
			want := map[string]PriceWeight{"opus": {Input: 1, Output: 2, CacheWrite: 3, CacheWrite1h: 5, CacheRead: 4}}
			if !reflect.DeepEqual(c.Usage.PriceWeights, want) {
				t.Fatalf("price weights = %+v (negative entries should be dropped)", c.Usage.PriceWeights)
			}
		}},
		{name: "log_days 0 keeps everything", content: ptr("[models]\nlog_days = 0\n"), check: func(t *testing.T, c Config) {
			if c.Models.LogDays != 0 {
				t.Fatalf("log_days = %d", c.Models.LogDays)
			}
		}},
		{name: "negative log_days falls back", content: ptr("[models]\nlog_days = -5\n"), check: func(t *testing.T, c Config) {
			if c.Models.LogDays != 90 {
				t.Fatalf("log_days = %d", c.Models.LogDays)
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

func TestLoadWarnsAboutUnknownKeysAndKeepsTheRest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[ui]\ncolors = \"24BIT\"\n[updates]\ncheck = true\n[update]\ncheck = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if !IsWarning(err) || !strings.Contains(err.Error(), "update.check") {
		t.Fatalf("err = %v, want a warning naming update.check", err)
	}
	if cfg.UI.Colors != "24bit" || !cfg.Updates.Check {
		t.Fatalf("settings lost: colors=%q check=%v", cfg.UI.Colors, cfg.Updates.Check)
	}
}
