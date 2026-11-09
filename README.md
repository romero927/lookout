# Lookout

A terminal dashboard that runs **beside** an interactive [Claude Code](https://docs.anthropic.com/en/docs/claude-code) session, for example in a tmux split. It shows token usage, suggests when to switch models, lets you browse your config and memory files, and can optionally have Codex cross-check Claude's replies.

> **Unofficial.** Lookout is an independent open-source project. It is not affiliated with, endorsed by, or supported by Anthropic or OpenAI. "Claude" and "Claude Code" are trademarks of Anthropic; "Codex" and "ChatGPT" are trademarks of OpenAI.

![Lookout screenshot](docs/screenshot.png)
<!-- Screenshot placeholder: replace docs/screenshot.png with a real capture. -->

## What it is (and isn't)

Lookout is a **dashboard, not a wrapper**:

- It **never calls Claude**. It doesn't use `claude -p`, the Agent SDK, or the Anthropic API, and it never types into Claude Code through a pty or scripted input. All of your Claude usage stays on your normal interactive limits.
- It **only reads** what Claude Code writes to disk (`~/.claude/`), plus what Claude Code itself passes to an optional status line command and Stop hook.
- It **never handles credentials**. It doesn't read `~/.claude/.credentials.json` or `~/.codex/auth.json`, and the Files panel refuses to open them.
- The only external program it can run is `codex exec`, for the opt-in cross-check. That uses your own Codex CLI login.

## Panels

| # | Panel | What it shows |
|---|-------|---------------|
| 1 | **Usage** | Your real **5-hour and weekly limits** (percent used, percent left, reset time) with a pace projection: where you'll be at reset, or when you'll hit the limit. Below that: the current session and its context fill, token counts for the session, today and the last 5 hours, burn rate, usage by model, and the last 7 days. **Tokens and percentages only, never dollars.** |
| 2 | **Models** | The current model, a suggested model, and a one-line reason, from tunable rules. You switch yourself with `/model`. It also flags when `/compact` or a fresh session is worth it, and logs each suggestion together with the model you actually used next. |
| 3 | **Files** | Browse `~/.claude/settings.json`, the global and project `CLAUDE.md` files, auto-memory files, and `~/.codex/config.toml` / `AGENTS.md`. Read-only by default, with secret-looking values masked on screen. |
| 4 | **Cross-check** | Codex cross-check reviews of Claude's replies, with a verdict (OK / CONCERNS / PROBLEMS). **Off by default.** |

Keys: `1`–`4` or `tab` switch panels · `x` toggles cross-check · `↑↓`/`jk` move · `enter` focuses a viewer · `esc` goes back · `e` edits (if enabled) · `?` help · `q` quits.

## Install

Requires Go 1.26+ to build from source. With `GOTOOLCHAIN=auto`, the default, an older `go` downloads 1.26 automatically. The result is a single static binary (pure Go, no cgo).

```sh
go install github.com/romero927/lookout/cmd/lookout@latest
# or
git clone https://github.com/romero927/lookout && cd lookout && make build   # → ./bin/lookout
```

Prebuilt binaries for Linux, macOS and Windows are attached to [releases](https://github.com/romero927/lookout/releases).

### Run it beside Claude Code

```sh
tmux split-window -h -l 50% 'lookout --here'  # focus on the session in this directory
# or just:
lookout                                        # follows your most recently active session
lookout stats                                  # one-shot text summary, no TUI
```

From a checkout, `scripts/split.sh [project-dir]` starts a tmux session with `claude` on the left and Lookout on the right. Running it again reattaches to that session.

### Show your real limits (recommended)

Claude Code passes your subscription's 5-hour and weekly usage (percent used and reset time) to its [status line](https://docs.anthropic.com/en/docs/claude-code/statusline) command. To have Lookout receive them, print the snippet and add it to `~/.claude/settings.json` yourself:

```sh
lookout statusline install --print
```

Claude Code then runs `lookout statusline` after each update. It saves only the limit numbers (one small file per session under `limits/` in the data directory, so sessions on different accounts stay separate) and prints a short line under your prompt, e.g. `Opus 5.5 · ctx 12% · 5h 42% (resets 2h23m) · week 23%`. Settings allow only one status line, so this replaces any you already have. Limits are reported for Claude.ai subscriptions only; without them, Lookout falls back to a token-based estimate.

### Optional: Codex cross-check

1. Install the [Codex CLI](https://github.com/openai/codex) and sign in with your ChatGPT plan, so that `codex` is on your `PATH`. Alternatively, set `crosscheck.codex_path`.
2. Print the hook snippet and **merge it yourself** into `~/.claude/settings.json`. Lookout never edits your settings.
   ```sh
   lookout hook install --print
   ```
   ```json
   {
     "hooks": {
       "Stop": [
         { "hooks": [ { "type": "command", "command": "/path/to/lookout hook stop", "timeout": 10 } ] }
       ]
     }
   }
   ```
3. Turn cross-check on with `x` in the dashboard, or `lookout crosscheck on`. Until then, the hook exits immediately and does nothing.

#### Exactly what the hook sends to Codex

When Claude Code finishes a reply, it runs `lookout hook stop`. If cross-check is **off** (the flag file is absent), the hook reads its input, discards it and exits. If it's **on**:

1. **Input.** The hook takes `last_assistant_message` from Claude Code's Stop hook input: the text of Claude's final reply. Tool calls, tool output, thinking and earlier turns are not included. If that field is missing, it falls back to the last text block Claude wrote after your most recent prompt in the session transcript.
2. **Size limit.** The text is truncated to `crosscheck.max_chars` characters (default 20,000).
3. **Prompt.** Codex receives exactly this on stdin and nothing else: the review instructions (the built-in prompt, or `crosscheck.prompt` if you set one), a blank line, then `<claude_response>`, the reply text, and `</claude_response>`. The built-in prompt asks for a `VERDICT:` line and at most 5 findings. It is `DefaultPrompt` in [`internal/crosscheck/worker.go`](internal/crosscheck/worker.go).
4. **Command.** It runs in a detached background process. The hook itself exits in milliseconds and never blocks Claude Code.
   ```
   codex exec --json --skip-git-repo-check --ephemeral --sandbox read-only \
     --cd <dir> --output-last-message <tmpfile> [--model <crosscheck.model>] -
   ```
   `<dir>` is a new, empty temporary directory, so Codex can't see your project. Set `crosscheck.repo_access = true` to run it in the session's working directory instead; the read-only sandbox still applies. `--ephemeral` keeps Codex from saving the session.
5. **Result.** Codex's final message, its verdict and its token usage are saved as JSON in Lookout's data directory (`crosscheck/reviews/`), where the Cross-check panel picks them up.

The hook never sends your prompts, other turns, files, environment or credentials. Codex calls use your Codex/ChatGPT plan, not Claude.

## Configuration

`lookout config init` writes a commented `config.toml` containing every default, and `lookout paths` shows where each file lives:

| | Linux | macOS |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/lookout/config.toml` (`~/.config/lookout`) | `~/Library/Application Support/lookout/config.toml` |
| Data (SQLite log, reviews, backups, logs) | `$XDG_DATA_HOME/lookout` (`~/.local/share/lookout`) | `~/Library/Application Support/lookout` |

Highlights:

- `[models]`: every threshold (repeated edits, tool errors, multi-file turns, long prompts, frustration phrases, simple-turn streaks, short prompts, context %) and the model tier order (`model_tiers = ["haiku", "sonnet", "opus"]`).
- `[files] allow_edit = true` turns on `e`, which opens the file in `$VISUAL`/`$EDITOR`. A timestamped backup is written to the data directory's `backups/` first, and is discarded if you didn't change anything.
- `[crosscheck]`: `codex_path`, `model`, `timeout_seconds`, `max_chars`, `max_parallel`, `repo_access`, `prompt`.
- `[ui] theme`: `auto`, `dark` or `light`. Inside tmux the terminal can't be asked, so dark is assumed. `LOOKOUT_THEME` overrides it.

Lookout honours `CLAUDE_CONFIG_DIR` and `CODEX_HOME`.

## How the numbers are computed

- **Source.** JSONL transcripts under `~/.claude/projects/<project>/<session>.jsonl`, plus `…/<session>/subagents/*.jsonl` for subagents. Usage comes from `message.usage` on `assistant` records, and the model from `message.model`.
- **Deduplication.** Claude Code writes one API response as several lines (one per content block), each repeating the same usage. Lookout counts each `message.id` once.
- **Limits.** The percentages and reset times are exactly what Claude Code passes to the status line. They cover all your Claude usage, on any device, but only update when Claude Code refreshes its status line. The 5-hour pace is the change over the last 30 minutes (idle time counts); the weekly pace is the average since the week started. The projection assumes that pace holds.
- **Usage block (estimated, fallback).** Shown only when real limits aren't available: a 5-hour window that starts at the first message after a break, floored to the hour. It is a heuristic, not Anthropic's actual limit meter.
- **Context.** Context ≈ input + cache read + cache write of the latest main-chain response. If a session goes past the configured window (default 200k), a 1M window is assumed.

### ⚠️ The transcript format is undocumented

Claude Code's transcript format isn't a public API and can change with any Claude Code update. Lookout reads only the fields it needs, ignores unknown fields and record types, skips malformed lines, and never crashes on bad input. If recent lines start failing to parse, a **⚑ PARSER** warning appears in the header and the token figures may be incomplete until Lookout is updated. Lookout was developed against Claude Code 2.1.x.

## Development

```sh
make build    # static binary in ./bin
make test     # go test ./... (race detector on)
make lint     # golangci-lint, or go vet + gofmt if it isn't installed
```

All test fixtures are **synthetic**. Never commit real transcripts. To benchmark against your own data locally without copying it into the repo:

```sh
LOOKOUT_BENCH_FILE=~/.claude/projects/<project>/<session>.jsonl go test ./internal/transcript -bench ReadFile
LOOKOUT_BENCH_LOAD=1 go test ./internal/app -bench Load
```

## License

[MIT](LICENSE)
