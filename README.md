# Lookout

A terminal dashboard that runs **beside** an interactive [Claude Code](https://docs.anthropic.com/en/docs/claude-code) session, for example in a tmux split. It shows your usage limits and token usage, which files changed, what Claude did in each turn, what's filling the context window, suggests when to switch models, and lets you browse your config and memory files.

> **Unofficial.** Lookout is an independent open-source project. It is not affiliated with, endorsed by, or supported by Anthropic. "Claude" and "Claude Code" are trademarks of Anthropic.

![Lookout's Usage panel, rendered from synthetic data](docs/screenshot.png)

## What it is (and isn't)

Lookout is a **dashboard, not a wrapper**:

- It **never calls Claude**. It doesn't use `claude -p`, the Agent SDK, or the Anthropic API, and it never types into Claude Code through a pty or scripted input. All of your Claude usage stays on your normal interactive limits.
- It **only reads** what Claude Code writes to disk (`~/.claude/`), plus what Claude Code itself passes to an optional status line command.
- It **never handles credentials**. It doesn't read `~/.claude/.credentials.json`, and the Files panel refuses to open it.
- It **sends nothing anywhere**. No network calls, no telemetry. The only programs it starts are `git` (read-only `diff`, `status` and `log`, for the Changes panel), a desktop notifier for alerts, a clipboard tool when you copy a command, and your `$EDITOR` if you enable editing.
- It **never types into Claude Code**. Commands it offers are copied for you to paste. Copying goes to the OS clipboard through `xclip`, `xsel`, `wl-copy`, `pbcopy` or `clip.exe`. On Linux, install one (`sudo apt install xclip`); without one, Lookout falls back to tmux's paste buffer (paste with your prefix, then `]`) or the terminal's clipboard, and says so. The mouse works too: click a tab, or an item to open it, scroll what's under the pointer, and drag to select text, which is copied when you let go (hold Shift for the terminal's own selection).

## Panels

| # | Panel | What it shows |
|---|-------|---------------|
| 1 | **Usage** | Your real **5-hour and weekly limits** (percent used, percent left, reset time) with a pace projection: where you'll be at reset, or when you'll hit the limit. Below that: the current session and its context fill, token counts for the session, today and the last 5 hours, burn rate, usage by model, the last 7 days, and how much of the weekly limit each day used. Tokens and percentages, not dollars, except on pay-as-you-go accounts, where it shows the session cost Claude Code reports. |
| 2 | **Activity** | One entry per prompt you sent, with everything that happened in order: your prompt, Claude's messages (with basic markdown), each tool call (failures marked, with the error), edits with their line counts, messages you sent while Claude was working, and subagent work grouped under the task it was given. Claude's latest message and the files it edited are at the top. Click a tool call (or press `x` for all of them) to see its full command or input and its output, read from the transcript when you open it. |
| 3 | **Changes** | Every file that changed during the session, in three groups: files Claude edited with its edit tools (including through subagents), with estimated lines added and removed; other uncommitted files; and files committed since the session started. The last two cover the session's own repository and any other repository it worked in (found from `cd` and `git -C` in its commands, and from the files it edited), so changes made by shell commands in other folders show up too. When more than one repository is involved, each group lists its files under the repository they belong to. Selecting a file shows its uncommitted `git diff`, its commits from this session, or, for a new file, its contents. For a file outside any git repository, it shows the edits Claude's tools made to it this session, read from the transcript. `o` opens its folder; `y` copies its commit ID. |
| 4 | **Context** | What fills the context window: the startup cost (system prompt, tools, `CLAUDE.md`, memory), then tool output by tool, files Claude wrote, replies and prompts, with the largest single items and a tip when one kind dominates. `a` lists every item; `enter` shows an item's details and how to free up room: `/rewind` to before it, or `/compact`. The total is what Claude Code reports; the parts are estimates. Counted since the session started or the last `/compact`. |
| 5 | **Models** | The current model, a suggested model, and a one-line reason, from tunable rules. Near a usage limit it suggests a lighter model to make the rest last, and holds off on upgrades that would use the limit up faster. New suggestions have to hold for 2 minutes or 2 turns before they replace the current one, so it doesn't flip back and forth. You switch yourself with `/model`; the panel lists each model and what it's for, in Claude Code's own words. It also flags when `/compact` or a fresh session is worth it, and logs each suggestion with the model you used next and, when you followed it, whether the next 3 turns went smoothly. |
| 6 | **Cheatsheet** | Claude Code commands to copy and paste: sessions (`/resume`, the session ID and `claude --resume <id>` for the session on screen, `/branch`, `/clear`), context (`/compact`, `/rewind`, `/context`), model and usage, and project setup. The top lists commands for the session in view, such as the suggested `/model`. `enter` copies. |
| 7 | **Skills** | Skills, custom slash commands and subagents Claude Code can use: yours (`~/.claude`), the project's (`.claude/`), installed plugins', and skills synced from claude.ai. Shows what each does, whether Claude uses it on its own, and how to run it (`c` copies that), plus its definition file. |
| 8 | **Files** | Browse `~/.claude/settings.json`, the global and project `CLAUDE.md` files, and auto-memory files. Read-only by default, with secret-looking values masked on screen. `o` opens the selected file's folder in your file manager. |

Keys: `1`–`8` or `tab` switch panels · `s` picks which session to show (`a` in the picker follows the latest again) · `↑↓`/`jk` move · `enter` focuses a viewer · `esc` goes back · `c` copies (Context, Skills, Cheatsheet) · `y` copies an ID (a file's commit in Changes, a session in the picker) · `e` edits (if enabled) · `R` restarts · `?` help · `q` quits.

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
tmux split-window -h -l 50% 'lookout --here'  # focus on the session in this directory (or a parent or subfolder)
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

Claude Code then runs `lookout statusline` after each update. It saves only a few numbers: limit percentages and reset times, context use, and the session cost. They go in one small file per session under `limits/` in the data directory, so sessions on different accounts stay separate, plus a per-day total in `history.json`. It also prints a short line under your prompt, e.g. `Opus 5.5 · ctx 12% · 5h 42% (resets 2h23m) · week 23%`. Settings allow only one status line, so this replaces any you already have. Limits are reported for Claude.ai subscriptions only.

**API key or pay-as-you-go accounts** have no 5-hour or weekly limits, so Lookout shows the session's cost so far instead (in the Usage panel and the status line, e.g. `Opus 5.5 · ctx 12% · $1.84`). Alerts, the per-day limit chart and limit-aware model suggestions stay off; everything else works the same.

To stop using it, remove the `statusLine` entry from `~/.claude/settings.json`, then delete the binary and Lookout's config and data folders (`lookout paths` lists them).

## Configuration

`lookout config init` writes a commented `config.toml` containing every default, and `lookout paths` shows where each file lives:

| | Linux | macOS |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/lookout/config.toml` (`~/.config/lookout`) | `~/Library/Application Support/lookout/config.toml` |
| Data (suggestion log, limits, alert state, backups, logs) | `$XDG_DATA_HOME/lookout` (`~/.local/share/lookout`) | `~/Library/Application Support/lookout` |

Highlights:

- `[models]`: every threshold (repeated edits, tool errors, multi-file turns, long prompts, frustration phrases, simple-turn streaks, short prompts, context %) and the model tier order (`model_tiers = ["haiku", "sonnet", "opus"]`).
- `[files] allow_edit = true` turns on `e`, which opens the file in `$VISUAL`/`$EDITOR`. A timestamped backup is written to the data directory's `backups/` first, and is discarded if you didn't change anything.
- `[alerts]`: `bell`, `desktop`, `thresholds` (default `[80, 95]`; `[]` turns them off) and `pace`.
- `[ui] theme`: `auto`, `dark` or `light`. Inside tmux the terminal can't be asked, so dark is assumed. `LOOKOUT_THEME` overrides it.

Lookout honours `CLAUDE_CONFIG_DIR`.

## How the numbers are computed

- **Source.** JSONL transcripts under `~/.claude/projects/<project>/<session>.jsonl`, plus `…/<session>/subagents/*.jsonl` for subagents. Usage comes from `message.usage` on `assistant` records, and the model from `message.model`.
- **Deduplication.** Claude Code writes one API response as several lines (one per content block), each repeating the same usage. Lookout counts each `message.id` once.
- **Limits.** The percentages and reset times are exactly what Claude Code passes to the status line. They cover all your Claude usage, on any device, but only update when Claude Code refreshes its status line. The 5-hour pace is the change over the last 30 minutes (idle time counts); the weekly pace is the average since the week started. The projection assumes that pace holds.
- **Weekly limit per day** (in Last 7 days). Each time Claude Code reports limits, the rise since the previous reading is added to that local day. After a window resets, the new window's usage counts in full. The first reading ever only sets a starting point, so the chart fills in from the day you connect the status line.
- **Usage block (estimated, fallback).** Shown only when the status line isn't connected yet: a 5-hour window that starts at the first message after a break, floored to the hour. It is a heuristic, not Anthropic's actual limit meter.
- **Context.** With the status line connected, this is the percentage Claude Code itself reports. Otherwise it is estimated: input + cache read + cache write of the latest main-chain response, against the configured window (default 200k, or 1M once a session goes past it).

### ⚠️ The transcript format is undocumented

Claude Code's transcript format isn't a public API and can change with any Claude Code update. Lookout reads only the fields it needs, ignores unknown fields and record types, skips malformed lines, and never crashes on bad input. If recent lines start failing to parse, a **! PARSER** warning appears in the header and the token figures may be incomplete until Lookout is updated. Lookout was developed against Claude Code 2.1.x.

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
