package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "claude")
	proj := filepath.Join(root, "proj")
	write(t, filepath.Join(claude, "skills", "notes", "SKILL.md"), "---\nname: notes\ndescription: >\n  Keep meeting notes\n  tidy.\nargument-hint: [topic]\n---\n# Notes\n")
	write(t, filepath.Join(claude, "skills", "synced", "abc", "pdf", "SKILL.md"), "---\nname: pdf\ndescription: \"Work with PDFs\"\n---\n")
	write(t, filepath.Join(claude, "commands", "standup.md"), "# Write my standup update\nbody")
	write(t, filepath.Join(claude, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews diffs\n---\n")
	write(t, filepath.Join(proj, ".claude", "skills", "deploy", "SKILL.md"), "---\nname: deploy\ndescription: Ship it\ndisable-model-invocation: true\n---\n")

	items := Discover(claude, []string{proj, proj})
	byName := map[string]Item{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if len(items) != 5 {
		t.Fatalf("got %d items: %+v", len(items), items)
	}
	if it := byName["notes"]; it.Description != "Keep meeting notes tidy." || it.Invoke() != "/notes " || it.Source != SourceUser {
		t.Errorf("notes: %+v invoke %q", it, it.Invoke())
	}
	if it := byName["pdf"]; it.Source != SourceSynced || it.Description != "Work with PDFs" || it.Invoke() != "Use the pdf skill to " {
		t.Errorf("pdf: %+v", it)
	}
	if it := byName["standup"]; it.Kind != KindCommand || it.Description != "Write my standup update" || it.Invoke() != "/standup" {
		t.Errorf("standup: %+v", it)
	}
	if it := byName["reviewer"]; it.Kind != KindAgent || it.Invoke() != "Use the reviewer subagent to " {
		t.Errorf("reviewer: %+v", it)
	}
	if it := byName["deploy"]; it.ModelInvocable || it.Source != SourceProject {
		t.Errorf("deploy: %+v", it)
	}
	if items[0].Name != "deploy" {
		t.Errorf("project skills should come first: %v", items[0].Name)
	}
}

func TestPlugins(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "claude")
	inst := filepath.Join(root, "cache", "fmt")
	write(t, filepath.Join(inst, "skills", "tidy", "SKILL.md"), "---\nname: tidy\ndescription: Tidy code\n---\n")
	write(t, filepath.Join(inst, "commands", "lint.md"), "---\ndescription: Run the linter\n---\n")
	off := filepath.Join(root, "cache", "old")
	write(t, filepath.Join(off, "agents", "helper.md"), "---\nname: helper\ndescription: Helps\n---\n")
	write(t, filepath.Join(claude, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fmt@market":[{"scope":"user","installPath":"`+inst+`"}],"old@market":[{"installPath":"`+off+`"}]}}`)
	write(t, filepath.Join(claude, "settings.json"), `{"enabledPlugins":{"fmt@market":true,"old@market":false}}`)

	byName := map[string]Item{}
	for _, it := range Discover(claude, nil) {
		byName[it.Name] = it
	}
	if it := byName["tidy"]; it.Plugin != "fmt" || it.Source != SourcePlugin || it.Invoke() != "/fmt:tidy" || it.Disabled {
		t.Errorf("tidy: %+v %q", it, it.Invoke())
	}
	if it := byName["lint"]; it.Kind != KindCommand || it.Invoke() != "/fmt:lint" {
		t.Errorf("lint: %+v", it)
	}
	if it := byName["helper"]; !it.Disabled || it.Plugin != "old" {
		t.Errorf("helper: %+v", it)
	}
}
