// Package skills lists what Claude Code can use besides its built-in tools:
// skills, custom slash commands and subagents, from the user's ~/.claude
// and the project's .claude folder. Only the front matter is read.
package skills

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kinds and sources.
const (
	KindSkill   = "Skill"
	KindCommand = "Command"
	KindAgent   = "Subagent"

	SourceUser    = "yours"
	SourceSynced  = "synced from claude.ai"
	SourceProject = "this project"
	SourcePlugin  = "plugin"
)

// Item is one skill, command or subagent.
type Item struct {
	Kind        string
	Source      string
	Name        string
	Description string
	WhenToUse   string
	ArgHint     string
	Path        string // the file that defines it
	Plugin      string // the plugin it comes from, for SourcePlugin
	Disabled    bool   // its plugin is turned off in settings
	// UserInvocable: you can run it as /name.
	UserInvocable bool
	// ModelInvocable: Claude may use it on its own when it fits.
	ModelInvocable bool
}

// Invoke is what to paste into Claude Code to use the item.
func (it Item) Invoke() string {
	switch {
	case it.Kind == KindAgent:
		return "Use the " + it.Name + " subagent to "
	case it.Source == SourceSynced || !it.UserInvocable:
		return "Use the " + it.Name + " skill to "
	default:
		s := "/" + it.Name
		if it.Plugin != "" {
			s = "/" + it.Plugin + ":" + it.Name
		}
		if it.ArgHint != "" {
			s += " "
		}
		return s
	}
}

// Discover finds items under claudeDir (~/.claude) and each project folder.
func Discover(claudeDir string, projects []string) []Item {
	var out []Item
	seen := map[string]bool{}
	add := func(it Item, ok bool) {
		if ok && !seen[it.Path] {
			seen[it.Path] = true
			out = append(out, it)
		}
	}
	scanSkills := func(dir, source string) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
		for _, p := range matches {
			add(read(p, KindSkill, source, filepath.Base(filepath.Dir(p))))
		}
	}
	scanFlat := func(dir, kind, source string) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.md"))
		for _, p := range matches {
			add(read(p, kind, source, strings.TrimSuffix(filepath.Base(p), ".md")))
		}
	}

	scanSkills(filepath.Join(claudeDir, "skills"), SourceUser)
	synced, _ := filepath.Glob(filepath.Join(claudeDir, "skills", "synced", "*"))
	for _, d := range synced {
		scanSkills(d, SourceSynced)
	}
	scanFlat(filepath.Join(claudeDir, "commands"), KindCommand, SourceUser)
	scanFlat(filepath.Join(claudeDir, "agents"), KindAgent, SourceUser)
	for _, pl := range installedPlugins(claudeDir) {
		before := len(out)
		base := pl.path
		scanSkills(filepath.Join(base, "skills"), SourcePlugin)
		scanFlat(filepath.Join(base, "commands"), KindCommand, SourcePlugin)
		scanFlat(filepath.Join(base, "agents"), KindAgent, SourcePlugin)
		for i := before; i < len(out); i++ {
			out[i].Plugin, out[i].Disabled = pl.name, pl.disabled
		}
	}
	for _, p := range projects {
		if p == "" {
			continue
		}
		base := filepath.Join(p, ".claude")
		scanSkills(filepath.Join(base, "skills"), SourceProject)
		scanFlat(filepath.Join(base, "commands"), KindCommand, SourceProject)
		scanFlat(filepath.Join(base, "agents"), KindAgent, SourceProject)
	}
	order := map[string]int{SourceProject: 0, SourceUser: 1, SourcePlugin: 2, SourceSynced: 3}
	kinds := map[string]int{KindSkill: 0, KindCommand: 1, KindAgent: 2}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if kinds[a.Kind] != kinds[b.Kind] {
			return kinds[a.Kind] < kinds[b.Kind]
		}
		if order[a.Source] != order[b.Source] {
			return order[a.Source] < order[b.Source]
		}
		if a.Plugin != b.Plugin {
			return a.Plugin < b.Plugin
		}
		return a.Name < b.Name
	})
	return out
}

// read parses the front matter of one definition file.
func read(path, kind, source, fallback string) (Item, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Item{}, false
	}
	defer f.Close()
	it := Item{Kind: kind, Source: source, Name: fallback, Path: path, UserInvocable: true, ModelInvocable: true}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	inFront, first := false, true
	var lastKey string
	for n := 0; sc.Scan() && n < 200; n++ {
		line := sc.Text()
		if first {
			first = false
			if strings.TrimSpace(line) == "---" {
				inFront = true
				continue
			}
		}
		if !inFront {
			// No front matter: the first text line describes it.
			if t := strings.TrimSpace(strings.TrimLeft(line, "#")); t != "" && it.Description == "" {
				it.Description = t
				break
			}
			continue
		}
		if strings.TrimSpace(line) == "---" {
			break
		}
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && lastKey != "" {
			// A folded continuation line.
			cont := strings.TrimSpace(line)
			switch lastKey {
			case "description":
				it.Description = strings.TrimSpace(it.Description + " " + cont)
			case "when_to_use":
				it.WhenToUse = strings.TrimSpace(it.WhenToUse + " " + cont)
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), unquote(strings.TrimSpace(v))
		lastKey = k
		switch k {
		case "name":
			if v != "" {
				it.Name = v
			}
		case "description":
			it.Description = v
		case "when_to_use":
			it.WhenToUse = v
		case "argument-hint":
			it.ArgHint = v
		case "user-invocable":
			it.UserInvocable = v != "false"
		case "disable-model-invocation":
			it.ModelInvocable = v != "true"
		}
	}
	return it, true
}

func unquote(s string) string {
	if s == ">" || s == "|" || s == ">-" || s == "|-" {
		return ""
	}
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

type plugin struct {
	name, path string
	disabled   bool
}

// installedPlugins reads ~/.claude/plugins/installed_plugins.json (both the
// older and the current layout: any object with an installPath under a
// "name@marketplace" key) and the enabledPlugins switches in settings.json.
func installedPlugins(claudeDir string) []plugin {
	b, err := os.ReadFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var doc any
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	enabled := map[string]bool{}
	if sb, err := os.ReadFile(filepath.Join(claudeDir, "settings.json")); err == nil {
		var st struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		_ = json.Unmarshal(sb, &st)
		enabled = st.EnabledPlugins
	}
	var out []plugin
	seen := map[string]bool{}
	var walk func(id string, v any)
	walk = func(id string, v any) {
		switch t := v.(type) {
		case map[string]any:
			if p, ok := t["installPath"].(string); ok && p != "" && id != "" {
				if !seen[p] {
					seen[p] = true
					name, _, _ := strings.Cut(id, "@")
					on, listed := enabled[id]
					out = append(out, plugin{name: name, path: p, disabled: listed && !on})
				}
				return
			}
			for k, c := range t {
				next := id
				if strings.Contains(k, "@") {
					next = k
				}
				walk(next, c)
			}
		case []any:
			for _, c := range t {
				walk(id, c)
			}
		}
	}
	walk("", doc)
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
