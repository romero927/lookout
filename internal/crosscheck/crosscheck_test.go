package crosscheck

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func dirs(t *testing.T) Dirs {
	root := t.TempDir()
	return Dirs{
		Flag:    filepath.Join(root, "crosscheck.on"),
		Reviews: filepath.Join(root, "reviews"),
		Jobs:    filepath.Join(root, "jobs"),
	}
}

func TestToggle(t *testing.T) {
	d := dirs(t)
	if Enabled(d.Flag) {
		t.Fatal("cross-check must default to off")
	}
	on, err := Toggle(d.Flag)
	if err != nil || !on || !Enabled(d.Flag) {
		t.Fatalf("toggle on: %v %v", on, err)
	}
	on, err = Toggle(d.Flag)
	if err != nil || on || Enabled(d.Flag) {
		t.Fatalf("toggle off: %v %v", on, err)
	}
	if err := SetEnabled(d.Flag, false); err != nil {
		t.Fatal("disabling twice must be fine")
	}
}

func TestHandleStop(t *testing.T) {
	transcriptPath := filepath.Join(t.TempDir(), "t.jsonl")
	_ = os.WriteFile(transcriptPath, []byte(strings.Join([]string{
		`{"type":"user","timestamp":"2026-01-10T10:00:00Z","message":{"content":"first question"}}`,
		`{"type":"assistant","timestamp":"2026-01-10T10:00:01Z","message":{"id":"m1","model":"x","content":[{"type":"text","text":"old answer"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"user","timestamp":"2026-01-10T10:01:00Z","message":{"content":"second question"}}`,
		`{"type":"assistant","timestamp":"2026-01-10T10:01:01Z","message":{"id":"m2","model":"x","content":[{"type":"text","text":"working on it"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"assistant","timestamp":"2026-01-10T10:01:05Z","message":{"id":"m3","model":"x","content":[{"type":"text","text":"final answer from transcript"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
	}, "\n")+"\n"), 0o644)

	tests := []struct {
		name       string
		enabled    bool
		input      string
		maxPar     int
		preRunning int
		spawnErr   error
		wantQueued bool
		wantStatus string // status of the newest review, "" if none
		wantSent   string
		wantErr    bool
	}{
		{name: "off by default does nothing", input: `{"last_assistant_message":"hi"}`},
		{name: "queues last_assistant_message", enabled: true, input: `{"session_id":"s","cwd":"/w","hook_event_name":"Stop","last_assistant_message":"All tests pass."}`, wantQueued: true, wantStatus: StatusQueued, wantSent: "All tests pass."},
		{name: "falls back to transcript", enabled: true, input: `{"session_id":"s","transcript_path":"` + transcriptPath + `"}`, wantQueued: true, wantStatus: StatusQueued, wantSent: "final answer from tr"},
		{name: "empty message ignored", enabled: true, input: `{"session_id":"s","last_assistant_message":"   "}`},
		{name: "other events ignored", enabled: true, input: `{"hook_event_name":"SubagentStop","last_assistant_message":"x"}`},
		{name: "bad json is an error", enabled: true, input: `not json`, wantErr: true},
		{name: "truncates long responses", enabled: true, input: `{"last_assistant_message":"` + strings.Repeat("a", 50) + `"}`, wantQueued: true, wantStatus: StatusQueued, wantSent: strings.Repeat("a", 20)},
		{name: "skips when too many running", enabled: true, input: `{"last_assistant_message":"x"}`, maxPar: 1, preRunning: 1, wantStatus: StatusSkipped},
		{name: "spawn failure recorded", enabled: true, input: `{"last_assistant_message":"x"}`, spawnErr: errors.New("nope"), wantStatus: StatusError, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := dirs(t)
			if tt.enabled {
				_ = SetEnabled(d.Flag, true)
			}
			now := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
			for i := 0; i < tt.preRunning; i++ {
				_ = Save(d.Reviews, Review{ID: NewID(now.Add(-time.Second)), Status: StatusRunning, CreatedAt: now.Add(-time.Second)})
			}
			var spawned []string
			id, err := HandleStop(strings.NewReader(tt.input), HookOptions{
				Dirs: d, MaxChars: 20, MaxParallel: tt.maxPar, Timeout: time.Minute,
				Now:   func() time.Time { return now },
				Spawn: func(id string) error { spawned = append(spawned, id); return tt.spawnErr },
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if (id != "") != tt.wantQueued {
				t.Fatalf("id = %q", id)
			}
			if tt.wantQueued && (len(spawned) != 1 || spawned[0] != id) {
				t.Fatalf("spawned = %v", spawned)
			}
			rs, _ := List(d.Reviews, 0)
			var newest *Review
			for i := range rs {
				if rs[i].CreatedAt.Equal(now) {
					newest = &rs[i]
				}
			}
			if tt.wantStatus == "" {
				if newest != nil {
					t.Fatalf("unexpected review %+v", newest)
				}
				return
			}
			if newest == nil || newest.Status != tt.wantStatus {
				t.Fatalf("review = %+v", newest)
			}
			if tt.wantSent != "" {
				b, _ := os.ReadFile(filepath.Join(d.Jobs, newest.ID+".txt"))
				if string(b) != tt.wantSent {
					t.Fatalf("job = %q", b)
				}
			}
		})
	}
}

// fakeCodex writes a script that records its args and stdin, then behaves
// according to mode.
func fakeCodex(t *testing.T, mode string) (path, argsFile, stdinFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	argsFile, stdinFile = filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	path = filepath.Join(dir, "codex")
	script := `#!/bin/sh
printf '%s\n' "$@" > '` + argsFile + `'
cat > '` + stdinFile + `'
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--output-last-message" ]; then out="$a"; fi
  prev="$a"
done
case "` + mode + `" in
ok)
  echo '{"type":"thread.started","thread_id":"t"}'
  echo 'not json noise'
  printf '%s\n' '{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"VERDICT: CONCERNS\n- from events"}}'
  echo '{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":10,"output_tokens":20}}'
  printf 'VERDICT: CONCERNS\n- the test claim is unverified\n' > "$out"
  ;;
events-only)
  printf '%s\n' '{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"**Verdict:** OK\nLooks right."}}'
  ;;
fail)
  echo '{"type":"turn.failed","error":{"message":"not logged in"}}'
  echo 'auth problem' >&2
  exit 1
  ;;
slow)
  sleep 5
  ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, argsFile, stdinFile
}

func TestRunWorker(t *testing.T) {
	tests := []struct {
		mode       string
		timeout    time.Duration
		status     string
		verdict    string
		summaryHas string
		errHas     string
	}{
		{mode: "ok", status: StatusDone, verdict: "CONCERNS", summaryHas: "unverified"},
		{mode: "events-only", status: StatusDone, verdict: "OK", summaryHas: "Looks right"},
		{mode: "fail", status: StatusError, errHas: "not logged in"},
		{mode: "slow", timeout: 300 * time.Millisecond, status: StatusError, errHas: "timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			d := dirs(t)
			codex, argsFile, stdinFile := fakeCodex(t, tt.mode)
			now := time.Now()
			r := Review{ID: NewID(now), SessionID: "s", Cwd: t.TempDir(), Status: StatusQueued, CreatedAt: now}
			_ = Save(d.Reviews, r)
			_ = os.MkdirAll(d.Jobs, 0o700)
			_ = os.WriteFile(filepath.Join(d.Jobs, r.ID+".txt"), []byte("I fixed the bug and all tests pass."), 0o600)

			timeout := tt.timeout
			if timeout == 0 {
				timeout = 10 * time.Second
			}
			if err := Run(r.ID, WorkerOptions{Dirs: d, CodexPath: codex, Timeout: timeout, Model: "test-model"}); err != nil {
				t.Fatal(err)
			}
			got, _ := Load(d.Reviews, r.ID)
			if got.Status != tt.status || got.Verdict != tt.verdict {
				t.Fatalf("review = %+v", got)
			}
			if !strings.Contains(got.Summary, tt.summaryHas) || !strings.Contains(got.Error, tt.errHas) {
				t.Fatalf("summary=%q error=%q", got.Summary, got.Error)
			}
			if _, err := os.Stat(filepath.Join(d.Jobs, r.ID+".txt")); !os.IsNotExist(err) {
				t.Fatal("job file not cleaned up")
			}
			if tt.mode != "ok" {
				return
			}
			if got.Usage == nil || got.Usage.Input != 100 || got.Usage.Output != 20 {
				t.Fatalf("usage = %+v", got.Usage)
			}
			args, _ := os.ReadFile(argsFile)
			a := string(args)
			for _, want := range []string{"exec\n", "--json\n", "--sandbox\nread-only\n", "--ephemeral\n", "--skip-git-repo-check\n", "--model\ntest-model\n", "\n-\n"} {
				if !strings.Contains(a, want) {
					t.Fatalf("args missing %q:\n%s", want, a)
				}
			}
			if strings.Contains(a, r.Cwd) {
				t.Fatal("repo_access=false must not pass the project directory")
			}
			stdin, _ := os.ReadFile(stdinFile)
			if string(stdin) != BuildPrompt("", "I fixed the bug and all tests pass.") {
				t.Fatalf("stdin = %q", stdin)
			}
		})
	}
}

func TestRunWorkerRepoAccessAndMissingCodex(t *testing.T) {
	d := dirs(t)
	codex, argsFile, _ := fakeCodex(t, "ok")
	cwd := t.TempDir()
	now := time.Now()
	r := Review{ID: NewID(now), Cwd: cwd, Status: StatusQueued, CreatedAt: now}
	_ = Save(d.Reviews, r)
	_ = os.MkdirAll(d.Jobs, 0o700)
	_ = os.WriteFile(filepath.Join(d.Jobs, r.ID+".txt"), []byte("x"), 0o600)
	if err := Run(r.ID, WorkerOptions{Dirs: d, CodexPath: codex, RepoAccess: true}); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--cd\n"+cwd+"\n") {
		t.Fatalf("repo access should pass cwd:\n%s", args)
	}

	r2 := Review{ID: NewID(now.Add(time.Millisecond)), Status: StatusQueued, CreatedAt: now}
	_ = Save(d.Reviews, r2)
	_ = os.WriteFile(filepath.Join(d.Jobs, r2.ID+".txt"), []byte("x"), 0o600)
	_ = Run(r2.ID, WorkerOptions{Dirs: d, CodexPath: filepath.Join(t.TempDir(), "nope")})
	got, _ := Load(d.Reviews, r2.ID)
	if got.Status != StatusError || !strings.Contains(got.Error, "not found") {
		t.Fatalf("review = %+v", got)
	}
}

func TestParseVerdict(t *testing.T) {
	tests := map[string]string{
		"VERDICT: OK\nfine":             "OK",
		"verdict - problems":            "PROBLEMS",
		"**Verdict:** Concerns\n- x":    "CONCERNS",
		"Some preamble\nVERDICT: OK":    "OK",
		"no verdict here":               "",
		"The verdict: okay-ish, really": "",
	}
	for in, want := range tests {
		if got := ParseVerdict(in); got != want {
			t.Errorf("ParseVerdict(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEventsLegacyShape(t *testing.T) {
	ev := ParseEvents(strings.NewReader(`{"id":"0","msg":{"type":"agent_message","message":"VERDICT: OK"}}
{"id":"1","msg":{"type":"error","message":"rate limited"}}`))
	if ev.LastMessage != "VERDICT: OK" || ev.Error != "rate limited" {
		t.Fatalf("events = %+v", ev)
	}
}

func TestHookSnippet(t *testing.T) {
	s := HookSnippet("/usr/local/bin/lookout")
	for _, want := range []string{`"Stop"`, `"command": "/usr/local/bin/lookout hook stop"`, `"type": "command"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("snippet missing %s:\n%s", want, s)
		}
	}
	if got := HookCommand("/My Apps/lookout"); got != `'/My Apps/lookout' hook stop` {
		t.Fatalf("quoted = %s", got)
	}
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(p, []byte(s), 0o644)
	if !HookInstalled(p) {
		t.Fatal("snippet not detected as installed")
	}
	_ = os.WriteFile(p, []byte(`{"hooks":{}}`), 0o644)
	if HookInstalled(p) {
		t.Fatal("false positive")
	}
}

func TestListOrderAndInFlight(t *testing.T) {
	d := dirs(t)
	now := time.Now()
	_ = Save(d.Reviews, Review{ID: NewID(now.Add(-2 * time.Hour)), Status: StatusRunning, CreatedAt: now.Add(-2 * time.Hour)})
	_ = Save(d.Reviews, Review{ID: NewID(now.Add(-time.Minute)), Status: StatusDone, CreatedAt: now.Add(-time.Minute)})
	_ = Save(d.Reviews, Review{ID: NewID(now), Status: StatusQueued, CreatedAt: now})
	_ = os.WriteFile(filepath.Join(d.Reviews, "garbage.json"), []byte("{"), 0o600)
	rs, err := List(d.Reviews, 0)
	if err != nil || len(rs) != 3 || rs[0].Status != StatusQueued {
		t.Fatalf("list = %+v err=%v", rs, err)
	}
	if n := InFlight(d.Reviews, 10*time.Minute, now); n != 1 {
		t.Fatalf("in flight = %d (stale running review must not count)", n)
	}
}
