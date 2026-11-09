# Lookout

A terminal dashboard that runs **beside** an interactive [Claude Code](https://docs.anthropic.com/en/docs/claude-code) session, for example in a tmux split. It shows your usage limits and token usage, which files Claude changed, what it did in each turn, suggests when to switch models, and lets you browse your config and memory files.

> **Unofficial.** Lookout is an independent open-source project. It is not affiliated with, endorsed by, or supported by Anthropic. "Claude" and "Claude Code" are trademarks of Anthropic.

![Lookout's Usage panel, rendered from synthetic data](docs/screenshot.png)

## What it is (and isn't)

Lookout is a **dashboard, not a wrapper**:

- It **never calls Claude**. It doesn't use `claude -p`, the Agent SDK, or the Anthropic API, and it never types into Claude Code through a pty or scripted input. All of your Claude usage stays on your normal interactive limits.
- It **only reads** what Claude Code writes to disk (`~/.claude/`), plus what Claude Code itself passes to an optional status line command.
- It **never handles credentials**. It doesn't read `~/.claude/.credentials.json` or `~/.codex/auth.json`, and the Files panel refuses to open them.
- It **sends nothing anywhere**. No network calls, no telemetry. The only programs it starts are `git` (read-only `diff` and `status`, for the Changes panel), a desktop notifier for alerts, and your `$EDITOR` if you enable editing.

## Panels

| # | Panel | What it shows |
|---|-------|---------------|
| 1 | **Usage** | Your real **5-hour and weekly limits** (percent used, percent left, reset time) with a pace projection: where you'll be at reset, or when you'll hit the limit. Below that: the current session and its context fill, token counts for the session, today and the last 5 hours, burn rate, usage by model, the last 7 days, and how much of the weekly limit each day used. **Tokens and percentages only, never dollars.** |
| 2 | **Changes** | Every file Claude edited in the session (including through subagents), with estimated lines added and removed, and its uncommitted `git diff`. New files show in full. Other uncommitted files in the repository are listed below, so changes made by shell commands aren't missed. |
| 3 | **Activity** | One entry per prompt you sent: how long the turn took, the tools Claude used (failures marked), the files it edited, and its last message. Useful for catching up on what happened while you were away. |
| 4 | **Models** | The current model, a suggested model, and a one-line reason, from tunable rules. You switch yourself with `/model`. It also flags when `/compact` or a fresh session is worth it, and logs each suggestion together with the model you actually used next. |
| 5 | **Files** | Browse `~/.claude/settings.json`, the global and project `CLAUDE.md` files, auto-memory files, and `~/.codex/config.toml` / `AGENTS.md`. Read-only by default, with secret-looking values masked on screen. |

Keys: `1`–`5` or `tab` switch panels · `s` picks which session to show (`a` in the picker follows the latest again) · `↑↓`/`jk` move · `enter` focuses a viewer · `esc` goes back · `e` edits (if enabled) · `R` restarts · `?` help · `q` quits.

**Alerts.** When a limit passes 80% or 95%, or your pace would reach it before the reset, the dashboard rings the terminal bell (tmux marks the window), sends a desktop notification (`notify-send` on Linux, `osascript` on macOS) and shows the alert in the footer. Each alert fires once per limit window, even across restarts and several open dashboards. Tune or turn them off under `[alerts]`.

**New builds.** If the `lookout` binary is replaced while the dashboard runs (say, after `make build`), the header shows `↻ R restart`, and `R` restarts into the new build in place.

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

Claude Code then runs `lookout statusline` after each update. It saves only the limit numbers (one small file per session under `limits/` in the data directory, so sessions on different accounts stay separate, plus a per-day total in `history.json`) and prints a short line under your prompt, e.g. `Opus 5.5 · ctx 12% · 5h 42% (resets 2h23m) · week 23%`. Settings allow only one status line, so this replaces any you already have. Limits are reported for Claude.ai subscriptions only; without them, Lookout falls back to a token-based estimate.

## Configuration

`lookout config init` writes a commented `config.toml` containing every default, and `lookout paths` shows where each file lives:

| | Linux | macOS |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/lookout/config.toml` (`~/.config/lookout`) | `~/Library/Application Support/lookout/config.toml` |
| Data (SQLite log, reviews, backups, logs) | `$XDG_DATA_HOME/lookout` (`~/.local/share/lookout`) | `~/Library/Application Support/lookout` |

Highlights:

- `[models]`: every threshold (repeated edits, tool errors, multi-file turns, long prompts, frustration phrases, simple-turn streaks, short prompts, context %) and the model tier order (`model_tiers = ["haiku", "sonnet", "opus"]`).
- `[files] allow_edit = true` turns on `e`, which opens the file in `$VISUAL`/`$EDITOR`. A timestamped backup is written to the data directory's `backups/` first, and is discarded if you didn't change anything.
- `[alerts]`: `bell`, `desktop`, `thresholds` (default `[80, 95]`; `[]` turns them off) and `pace`.
- `[ui] theme`: `auto`, `dark` or `light`. Inside tmux the terminal can't be asked, so dark is assumed. `LOOKOUT_THEME` overrides it.

Lookout honours `CLAUDE_CONFIG_DIR` and `CODEX_HOME`.

## How the numbers are computed

- **Source.** JSONL transcripts under `~/.claude/projects/<project>/<session>.jsonl`, plus `…/<session>/subagents/*.jsonl` for subagents. Usage comes from `message.usage` on `assistant` records, and the model from `message.model`.
- **Deduplication.** Claude Code writes one API response as several lines (one per content block), each repeating the same usage. Lookout counts each `message.id` once.
- **Limits.** The percentages and reset times are exactly what Claude Code passes to the status line. They cover all your Claude usage, on any device, but only update when Claude Code refreshes its status line. The 5-hour pace is the change over the last 30 minutes (idle time counts); the weekly pace is the average since the week started. The projection assumes that pace holds.
- **Weekly limit by day.** Each time Claude Code reports limits, the rise since the previous reading is added to that local day. After a window resets, the new window's usage counts in full. The first reading ever only sets a starting point, so the chart fills in from the day you connect the status line.
- **Usage block (estimated, fallback).** Shown only when real limits aren't available: a 5-hour window that starts at the first message after a break, floored to the hour. It is a heuristic, not Anthropic's actual limit meter.
- **Context.** Context ≈ input + cache read + cache write of the latest main-chain response. If a session goes past the configured window (default 200k), a 1M window is assumed.

### ⚠️ The transcript format is undocumented

Claude Code's transcript format isn't a public API and can change with any Claude Code update. Lookout reads only the fields it needs, ignores unknown fields and record types, skips malformed lines, and never crashes on bad input. If recent lines start failing to parse, a **⚑ PARSER** warning appears in the header and the token figures may be incomplete until Lookout is updated. Lookout was developed against Claude Code 2.1.x.

## Development

```sh
make build    # static binary in ./bin
make test     # go test ./... (race detector on)
make lint     # golangci-lint, or go vet + gofmt if it isn't installed
make screenshot  # re-render docs/screenshot.png from synthetic data (needs Python 3 + Pillow)
```

All test fixtures are **synthetic**. Never commit real transcripts. To benchmark against your own data locally without copying it into the repo:

```sh
LOOKOUT_BENCH_FILE=~/.claude/projects/<project>/<session>.jsonl go test ./internal/transcript -bench ReadFile
LOOKOUT_BENCH_LOAD=1 go test ./internal/app -bench Load
```

## License

[MIT](LICENSE)
