# Panels

Full detail for each of Lookout's panels. Press `0`–`9` or `tab` to switch between them. The [README](../README.md#panels) has the short version.

## Which session is on screen

With nothing pinned, Lookout follows **the session you last sent a message to**: a prompt, a slash command, or a message typed while Claude was working. A session working on its own doesn't take over, so with several running the panels stay on the one you're talking to. The exception is a session that stops on a permission prompt or a question for you: Lookout switches to it (and says so at the bottom) until you answer, then goes back. Press `P` while it's on screen to stay on it. `--here` (or `--cwd`) limits this to sessions in that folder.

To look at another session: click its tab in the strip under the panel tabs, step through the tabs with `[` and `]`, or pick it in the [Sessions panel](#0-sessions) (`0` or `s`), which lists closed sessions too. Any session you pick, even the one Lookout is following, gets its own tab and is pinned (`◆` on its tab and `◆ pinned` in the header). A closed session shows `◷` and `◷ closed` instead: Lookout stays on it all the same. Auto keeps following in the background, and `↻ Auto`, `P`, or `a` in Sessions goes back to it. `lookout --session ID` (the start of the ID is enough) opens on one session. A pin survives `R` (restart).

The line under the tabs starts with the **`↻ Auto` tab**, lit while Lookout follows; the session it's following is shaded and marked with a blue `↻`. While a session is pinned, Auto turns grey and the pinned session's tab is bold, with `◆` (`◷` for a closed one). The other running sessions get a tab each: `●` working, `▲` needs you (a permission prompt or a question), `✓` finished while you were looking at another session, `○` idle, `◌` open with nothing sent yet. A closed session you picked comes right after Auto, before a `│`. With no other session running, it adds the session's folder and what it's doing.

**Opening a new Claude Code.** When Lookout runs in tmux, `+ claude` sits at the right end of that line. Click it (or press `N`) to start Claude Code in the folder of the session on screen. It splits that session's pane (side by side when the pane is at least 160 columns wide, otherwise one above the other), or opens a new tmux window when that session isn't in tmux or `[ui] new_claude = "window"`. tmux switches to the new pane, and on Auto, Lookout follows the new session.

Until Lookout finds a session (for example, `lookout --here` in a folder where Claude Code hasn't run yet), Activity, Changes, Context, Models and Tools show a placeholder saying which folder it's waiting on; `s` picks another session. The same happens when you start a new Claude Code session: it writes no transcript until you send the first message, so Lookout reads Claude Code's list of running sessions (`~/.claude/sessions`) and waits on the new one instead of showing the previous session. A pinned session stays on screen. Usage still shows your limits, and Skills, Config and the Cheatsheet work as usual.

## 0. Sessions

Every session in one place: the running ones first, in the order they started, then the 12 most recent closed ones (empty ones, such as a lone `/clear`, are left out). Sessions go by their title: the name you gave one (`/rename`), else the title Claude Code generates for it, else Claude Code's made-up name such as `lookout-3f`. A closed session is one line: when it was last active, its title, model, how many prompts you sent, files edited, how full its context was and, for pay-as-you-go accounts, what it cost as Claude Code reported it. Select one to see your last prompt to it, how long it ran, its tokens and its folder. A running session shows its state (as in the strip, with what it's doing, such as the command it's running, or what it waits for), model, how full its context is, its estimated share of your 5-hour limit, when it last did something, your last prompt to it, how many files it edited, its folder and its tmux pane. `▶` marks the session on screen.

`enter` (or a click) shows the session in Usage. `g` switches tmux to the pane the session runs in (when both it and Lookout run in tmux; Lookout only moves tmux's focus, it never types into the pane). `y` copies the session ID, `c` copies `claude --resume <id>`, `esc` goes back without changing anything.

Session states come from the files Claude Code keeps in `~/.claude/sessions` while it runs. They aren't documented, so if a Claude Code update changes them, Lookout falls back to the transcript: a session in the middle of a tool call is shown as working, anything else as idle, and "needs you" isn't shown.

With `[alerts] sessions = true` (the default), Lookout also alerts when a session other than the one on screen stops on a permission prompt or question, or finishes, with the bell and desktop notification set in `[alerts]`. Each alert fires once, even with several dashboards open.

## 1. Usage

Your real **5-hour and weekly limits** (percent used, percent left, reset time) with a pace projection: where you'll be at reset, or when you'll hit the limit, and an estimate of how much of each limit the current session used (its share of the Claude Code usage on this computer in that window, with tokens weighted by model price; usage on claude.ai or other computers isn't seen). Below that: the current session and its context fill, token counts for the session, today and the last 5 hours, burn rate, usage by model, the last 7 days, and how much of the weekly limit each day used. Tokens and percentages, not dollars, except on pay-as-you-go accounts: there it shows the session cost Claude Code reports, estimated dollars for today, the last 5 hours, each model and each day (tokens at API list prices, pay-as-you-go sessions only, marked `~`). The Limits heading links your usage page on claude.ai for every account (`o` opens it).

## 2. Activity

One entry per prompt you sent, with everything that happened in order: your prompt, Claude's messages (with basic markdown), each tool call (failures marked, with the error), edits with their line counts, messages you sent while Claude was working, and subagent work grouped under the task it was given. Each turn reads top to bottom: your prompt, what Claude did (Bash steps show the description Claude gave, and how long each step took), Claude's last message, then the files it edited. `n`/`p` move between turns, `/` searches prompts and replies. Click a tool call (or press `x` for all of them) to see its full command or input and its output, read from the transcript when you open it.

## 3. Changes

Every file that changed during the session, in three groups: files Claude edited with its edit tools (including through subagents), with estimated lines added and removed; other uncommitted files; and files committed since the session started (Claude's edits move here once they are committed, keeping their line counts). The last two cover the session's own repository and any other repository it worked in (found from `cd` and `git -C` in its commands, and from the files it edited), so changes made by shell commands in other folders show up too. When more than one repository is involved, each group lists its files under the repository they belong to. Selecting a file shows its uncommitted `git diff`, its commits from this session, or, for a new file, its contents. For a file outside any git repository, it shows the edits Claude's tools made to it this session, read from the transcript. Files in Claude Code's session scratchpad are left out, since they are throwaway; Activity still lists those edits. `o` opens its folder; `y` copies its commit ID.

Below the files, **Other actions** lists calls that probably changed something that isn't a file: `git push`, `gh pr create` and other GitHub writes, `curl` and `gh api` requests that POST, PUT, PATCH or DELETE, package publishes and global installs, `docker push`, `kubectl apply`, `terraform apply`, cloud CLI writes (`aws … create-…`, `gcloud … deploy`), deploy tools (Vercel, Netlify, Firebase, Fly, Wrangler), uploads with `scp` and `rsync`, service restarts, database writes from `psql` or `mysql`, and MCP tools whose names say they create, update, delete, send or post (tools named get, list, search and the like are left out). Each shows ✓ when it succeeded, ✗ when it failed, or … while it runs. Selecting one shows the full command or tool input and its output; `y` copies the command. Claude Code doesn't record what a call changed, so these are matched by command and tool name: a best guess that can miss an action, or include a call that only read.

## 4. Context

What fills the context window: the startup cost (system prompt, tools, `CLAUDE.md`, memory), then tool output by tool, files Claude wrote, replies and prompts, with the largest single items and a tip when one kind dominates. `a` lists every item; `enter` shows an item's details and how to free up room: `/rewind` to before it, or `/compact`. The total is what Claude Code reports; the parts are estimates. Counted since the session started or the last `/compact`.

## 5. Models

The current model and effort level, a suggested model or effort change, and a one-line reason, from tunable rules. A change of effort is suggested before a change of model, since it's the cheaper step (see [How it works](how-it-works.md#model-suggestions)). Near a usage limit it suggests a lighter model to make the rest last, and holds off on upgrades that would use the limit up faster. New suggestions have to hold for 2 minutes or 2 turns before they replace the current one, so it doesn't flip back and forth. You switch yourself with `/model` or `/effort`; the panel lists each model and what it's for, in Claude Code's own words, and each effort level. It also flags when `/compact` or a fresh session is worth it, and logs each suggestion with the model you used next and, when you followed it, whether the next 3 turns went smoothly (defined exactly in [How it works](how-it-works.md#what-went-smoothly-means)). The rules read your prompts locally; nothing leaves your computer.

## 6. Tools

Every tool Claude used in the session (Bash, Read, Edit, Agent, MCP tools and the rest), most used first, with how many calls errored and how many were denied (blocked by a permission rule, auto mode, a hook, or you) before they ran. Selecting a tool shows its total time and when it was last used, then each call (the Bash command's description, the file, the pattern) grouped under the prompt it belonged to. `enter` reads the calls: `↑↓` move between them, `enter` (or a click) opens one to show its full input and output, `a` shows it in Activity in the context of its prompt, and `f` shows failed calls only. Calls made by subagents are counted and labelled.

## 7. Skills

Skills, custom slash commands and subagents Claude Code can use: yours (`~/.claude`), the project's (`.claude/`), installed plugins', and skills synced from claude.ai. Shows what each does, whether Claude uses it on its own, and how to run it (`c` copies that), plus its definition file.

## 8. Config

Browse `~/.claude/settings.json`, the global and project `CLAUDE.md` files, auto-memory files, and Lookout's own `config.toml` (listed even before it exists, with how to create it; press `R` to restart after editing it). Read-only by default, with secret-looking values masked on screen. `o` opens the selected file's folder in your file manager.

## 9. Cheatsheet

Claude Code commands to copy and paste: sessions (`/resume`, the session ID and `claude --resume <id>` for the session on screen, `/branch`, `/clear`), context (`/compact`, `/rewind`, `/context`), model and usage, and project setup. The top lists commands for the session in view, such as the suggested `/model` or `/effort`. `enter` copies.
