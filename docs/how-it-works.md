# How Lookout works

Reference detail behind the [README](../README.md): what Lookout saves, how it works out each number, and how the Models panel decides what to suggest.

## What Lookout stores

Lookout writes only to its own config and data folders (`lookout paths` shows them). It never writes under `~/.claude/` (if you turn on editing, your `$EDITOR` does). Everything stays on your computer, and deleting the two folders removes it all.

| File | Folder | What's in it | Kept for |
|---|---|---|---|
| `config.toml` | config | Your settings. Written only by `lookout config init`. | Until you delete it |
| `lookout.db` (plus `-wal` and `-shm`) | data | SQLite log of model suggestions, one row each: time, session ID, current and suggested model, action, the reason and the signals behind it, context tokens, the model you used next and when, and the outcome (`smooth` or `rough`). The reason can name a file (base name only) and, for the short-prompt signal, quote your latest prompt if it is at most `short_prompt_chars` (default 40) characters. Readable only by you. | 90 days (`[models] log_days`; `0` keeps everything). Older rows are deleted when the dashboard starts |
| `limits/<session-id>.json` | data | What Claude Code passed to the status line for that session: 5-hour and weekly percentages and reset times, up to 720 one-a-minute samples (about 12 hours, for the pace), context use, and cost. Written by `lookout statusline`. | Deleted after 8 days without an update |
| `history.json` | data | Per local day: percentage points of the weekly and 5-hour limits used, and the day's 5-hour peak, plus the last reading of each limit window. Written by `lookout statusline`. | 35 days |
| `alerts.json` | data | Which limit alerts already fired (window, threshold, time), so they don't repeat. | Dropped a week after the window resets |
| `update.json` | data | The latest release tag and when it was checked (only with `[updates] check = true`). | Replaced by each check |
| `backups/<path>.<time>.bak` | data | A copy of a file taken before you edit it with `e` (only with `[files] allow_edit = true`). | Removed right away if you changed nothing; otherwise until you delete it |
| `logs/hook.log`, `logs/hook.log.1` | data | Status line problems: unreadable input, failed saves, and a wrapped command's failure (with that command). | Rotated at 1 MB (about 2 MB total) |

The first time it runs, Lookout also moves folders left by its old name (`overwatch`) to these locations.

## How the numbers are computed

- **Source.** Claude Code's transcripts: `~/.claude/projects/<project>/<session>.jsonl`, plus `…/<session>/subagents/*.jsonl` for subagents. Token usage comes from `message.usage` on `assistant` records, and the model from `message.model`.
- **No double counting.** Claude Code writes one API response as several lines (one per content block), each repeating the same usage. Lookout counts each `message.id` once.
- **Limits.** The percentages and reset times are exactly what Claude Code passes to the status line. They cover all your Claude usage on any device, but only update when Claude Code refreshes its status line.
- **Pace.** The 5-hour pace is the change over the last 30 minutes (idle time counts). The weekly pace is the average since the week started. The projection assumes the pace holds.
- **Weekly limit per day** (the "Last 7 days" chart). Each time Claude Code reports limits, the rise since the previous reading is added to that local day. After a window resets, the new window's usage counts in full. The very first reading only sets a starting point, so the chart fills in from the day you connect the status line.
- **This session's share of a limit.** Claude Code reports one percentage for the whole account, so Lookout splits it by this session's share of the Claude Code usage recorded on this computer in that window. Tokens are weighted by each model's API list price (input, output, cache write and cache read separately), so an Opus token counts for more than a Haiku token. Only the ratios matter here. They are hardcoded in `internal/usage/share.go` and updated by hand in releases; unknown models are weighted like Sonnet 4.6. Anthropic doesn't publish how each model counts against the limits, so this is an estimate. Override prices under `[usage.price_weights]` if they change before Lookout is updated.
- **Estimated cost** (pay-as-you-go accounts only). Claude Code reports a cost for the current session only, so the dollars for today, the last 5 hours, each model and each day are Lookout's estimates: each response's tokens priced at the same list prices, counting only sessions known to be pay-as-you-go (a cost but no limits in their saved status line data). Sessions on a subscription, or never seen by the status line, aren't priced. Discounts and negotiated rates aren't reflected.
- **Estimated usage block.** Shown only until the status line is connected: a 5-hour window starting at the first message after a break, rounded down to the hour. It is a guess, not Anthropic's actual limit meter.
- **Context.** With the status line connected, this is the percentage Claude Code reports. Otherwise it is estimated as input + cache read + cache write of the latest main-chain response, against the configured window (default 200k, or 1M once a session goes past 200k).

## Model suggestions

### Effort before model

`/effort` is a smaller, cheaper change than `/model`, so the Models panel tries it first.

- **Work looks hard** (repeated edits, failing tool results, frustration):
  - below `high` effort (the second-highest of `effort_levels`) → suggest one more level of effort
  - at `high` → suggest a stronger model
  - already on the top model → suggest one more level of effort, up to `xhigh`
- **Work looks simple**, on the top model or with effort above `default_effort` → suggest going back to `default_effort` before suggesting a lighter model.
- It never suggests `max`.
- Sessions with no recorded effort (older Claude Code, or models without effort levels) get model suggestions only.

### How your prompts are used

The rules read your prompts from the transcripts on disk, looking at:

- their length (`long_prompt_chars`, `short_prompt_chars`)
- whether the latest prompt contains one of the `frustration_phrases` (case-insensitive, and only on prompts of 300 characters or fewer, so a long request that quotes an error isn't treated as a complaint)

This all runs inside Lookout on your computer. No prompt text leaves the machine. The only place prompt text is kept is the suggestion log, as described in [What Lookout stores](#what-lookout-stores).

### What "went smoothly" means

The suggestion log marks a suggestion `smooth` or `rough` only when all of these are true:

1. It was an upgrade, a downgrade or an effort change (a "keep" is never judged).
2. You followed it: the first main-session response after the suggestion uses the suggested `/model` alias (e.g. `sonnet`), or, for an effort suggestion, the suggested effort level (e.g. `high`).
3. Three turns have happened since: the first 3 prompts in that session that got at least one response, counting from 1 minute before that first response.

Lookout then counts the failing tool results in those 3 turns. A result counts as failing when Claude Code marks it as an error, or when its output looks like a failing test, build or crash (`--- FAIL`, `FAIL`, `FAILED`, "N tests failed", a Python traceback, `AssertionError`, a Go `panic:`, `npm ERR!`, a Rust `error[E…]`, `BUILD FAILED`, "compilation failed"). Subagent tool calls aren't counted.

- Fewer than `tool_error_threshold` (default 3) → `smooth`
- That many or more → `rough`

Outcomes are judged by the dashboard while it shows that session, so a suggestion in a session you never open in Lookout stays unjudged.

## The transcript format is undocumented

Claude Code's transcript format isn't a public API and can change with any Claude Code update. Lookout reads only the fields it needs, ignores unknown fields and record types, skips malformed lines, and never crashes on bad input. If recent lines start failing to parse, a **! PARSER** warning appears in the header and token figures may be incomplete until Lookout is updated. Lookout was developed against Claude Code 2.1.x.
