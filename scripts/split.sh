#!/bin/sh
# Open Claude Code (left) and Lookout (right) side by side in tmux.
# Usage: scripts/split.sh [project-dir]
# Re-running it reattaches to the same session.
set -e

dir=$(cd "${1:-$PWD}" && pwd)
here=$(cd "$(dirname "$0")/.." && pwd)
lookout=${LOOKOUT_BIN:-$here/bin/lookout}
[ -x "$lookout" ] || lookout=$(command -v lookout) || {
	echo "lookout binary not found; run 'make build' first" >&2
	exit 1
}
session="lookout-$(basename "$dir" | tr -cd 'A-Za-z0-9_-')"

if ! tmux has-session -t "=$session" 2>/dev/null; then
	# Create the session at this terminal's size so the split
	# isn't rescaled on attach.
	size=$(stty size 2>/dev/null || echo "40 160")
	tmux new-session -d -s "$session" -c "$dir" \
		-x "${size#* }" -y "${size% *}" claude
	tmux set-option -t "$session" mouse on
	tmux split-window -h -l 50% -t "$session" -c "$dir" "'$lookout' --here"
	tmux select-pane -t "$session" -L
fi

if [ -n "$TMUX" ]; then
	exec tmux switch-client -t "$session"
fi
exec tmux attach -t "$session"
