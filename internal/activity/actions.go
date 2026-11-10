package activity

import (
	"strings"
	"time"
	"unicode"

	"github.com/romero927/lookout/internal/transcript"
)

// Action is a tool call that probably changed something outside the files
// Changes tracks: a push, a pull request, an API request that writes, a
// deploy, an MCP tool that creates or sends something. The transcript
// doesn't say what a call changed, so these are recognised by name and
// command: a best guess that can miss some and include a few that only read.
type Action struct {
	ID       string // tool_use ID, for loading the full input and output
	Tool     string // tool name
	Kind     string // what it does, e.g. "git push", "curl POST", "github: create_issue"
	Summary  string // the call's description or command
	At       time.Time
	Agent    string // the subagent that made the call, if any
	Subagent bool
	Fail     bool
	Done     bool   // its result has arrived
	Error    string // the start of a failed call's output
}

// maxActions caps how many actions are kept per session.
const maxActions = 500

// ActionKind returns what a tool call changes outside the tracked files, or
// "" when it doesn't look like it changes anything.
func ActionKind(tu transcript.ToolUse) string {
	if tu.Name == "Bash" || tu.Name == "PowerShell" {
		return commandAction(tu.Command)
	}
	if strings.HasPrefix(tu.Name, "mcp__") {
		return mcpAction(tu.Name)
	}
	return ""
}

// commandAction looks at each simple command in a shell command line and
// returns the first that changes something remote or system-wide.
func commandAction(cmd string) string {
	for _, seg := range splitCommands(cmd) {
		if k := segmentAction(shellWords(seg)); k != "" {
			return k
		}
	}
	return ""
}

// splitCommands splits a command line at ;, &&, ||, | and newlines. Quotes
// are respected; anything fancier (subshells, heredocs) is not.
func splitCommands(cmd string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ';' || r == '\n' || r == '|' || r == '&' || r == '(' || r == ')' || r == '`':
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return out
}

// shellWords splits a simple command into words, removing quotes, and
// drops leading variable assignments and wrappers like sudo and env.
func shellWords(seg string) []string {
	var words []string
	var b strings.Builder
	var quote rune
	in := false
	for _, r := range seg {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case unicode.IsSpace(r):
			if in {
				words = append(words, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteRune(r)
			in = true
		}
	}
	if in {
		words = append(words, b.String())
	}
	for len(words) > 0 {
		w := words[0]
		if opts, ok := wrappers[w]; ok {
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				f := words[0]
				words = words[1:]
				if opts[f] && len(words) > 0 {
					words = words[1:]
				}
			}
			if w == "timeout" && len(words) > 0 {
				words = words[1:] // the duration
			}
			continue
		}
		if isAssignment(w) {
			words = words[1:]
			continue
		}
		if i := strings.LastIndexAny(w, `/\`); i >= 0 {
			words[0] = w[i+1:] // /usr/bin/git -> git
		}
		return words
	}
	return nil
}

// wrappers run another command; the value lists their options that take a
// value, so it isn't mistaken for the command.
var wrappers = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-U": true, "-r": true, "-t": true},
	"doas":    {"-u": true, "-C": true},
	"env":     {"-u": true, "-C": true, "-S": true, "--unset": true, "--chdir": true},
	"xargs":   {"-I": true, "-n": true, "-P": true, "-L": true, "-d": true, "-E": true, "-s": true, "-a": true},
	"nice":    {"-n": true},
	"timeout": {"-s": true, "-k": true, "--signal": true, "--kill-after": true},
	"nohup":   {}, "time": {}, "command": {}, "exec": {}, "!": {}, "stdbuf": {}, "caffeinate": {},
}

// isAssignment reports whether w is a shell variable assignment (NAME=value).
func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// args returns the words after the program, without options. Options that
// take a value are given in valued, so their value is skipped too.
func args(words []string, valued ...string) []string {
	takes := map[string]bool{}
	for _, v := range valued {
		takes[v] = true
	}
	var out []string
	for i := 1; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			if takes[w] && !strings.Contains(w, "=") {
				i++
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

func first(a []string, n int) string {
	if n < len(a) {
		return a[n]
	}
	return ""
}

func oneOf(s string, list ...string) bool {
	for _, l := range list {
		if s == l {
			return true
		}
	}
	return false
}

func hasFlag(words []string, flags ...string) bool {
	for _, w := range words[1:] {
		for _, f := range flags {
			if w == f || strings.HasPrefix(w, f+"=") || (len(f) == 2 && strings.HasPrefix(w, f) && f[1] != '-') {
				return true
			}
		}
	}
	return false
}

// flagValue returns the value of the first of flags, as "-X POST", "-XPOST"
// or "--request=POST".
func flagValue(words []string, flags ...string) string {
	for i, w := range words {
		for _, f := range flags {
			switch {
			case w == f && i+1 < len(words):
				return words[i+1]
			case strings.HasPrefix(w, f+"="):
				return w[len(f)+1:]
			case len(f) == 2 && strings.HasPrefix(w, f) && len(w) > 2:
				return w[2:]
			}
		}
	}
	return ""
}

var writeMethods = map[string]bool{"POST": true, "PUT": true, "PATCH": true, "DELETE": true}

func segmentAction(w []string) string {
	if len(w) == 0 {
		return ""
	}
	prog := strings.TrimSuffix(strings.ToLower(w[0]), ".exe")
	switch prog {
	case "git":
		a := args(w, "-C", "-c", "--git-dir", "--work-tree")
		if first(a, 0) == "push" && !hasFlag(w, "--dry-run", "-n") {
			return "git push"
		}
	case "gh":
		a := args(w, "-R", "--repo", "-X", "--method", "-H", "--header", "-f", "-F", "--field", "--raw-field", "--input", "-q", "--jq", "-t", "--template", "--hostname")
		sub, verb := first(a, 0), first(a, 1)
		if sub == "api" {
			m := strings.ToUpper(flagValue(w, "-X", "--method"))
			if writeMethods[m] {
				return "gh api " + m
			}
			if m == "" && hasFlag(w, "-f", "-F", "--field", "--raw-field", "--input") {
				return "gh api POST"
			}
			return ""
		}
		writes := map[string][]string{
			"pr":       {"create", "merge", "close", "reopen", "edit", "comment", "review", "ready", "lock", "unlock"},
			"issue":    {"create", "close", "reopen", "edit", "comment", "delete", "transfer", "pin", "unpin", "lock", "unlock", "develop"},
			"release":  {"create", "edit", "delete", "upload", "delete-asset"},
			"repo":     {"create", "delete", "edit", "fork", "rename", "archive", "unarchive", "sync"},
			"workflow": {"run", "enable", "disable"},
			"run":      {"rerun", "cancel", "delete"},
			"secret":   {"set", "delete"},
			"variable": {"set", "delete"},
			"label":    {"create", "edit", "delete", "clone"},
			"gist":     {"create", "edit", "delete", "rename"},
			"cache":    {"delete"},
			"project":  {"create", "edit", "delete", "close", "item-add", "item-edit", "item-delete", "item-archive", "field-create", "field-delete"},
		}
		if oneOf(verb, writes[sub]...) {
			return "gh " + sub + " " + verb
		}
	case "curl":
		m := strings.ToUpper(flagValue(w, "-X", "--request"))
		switch {
		case writeMethods[m]:
			return "curl " + m
		case m == "" && hasFlag(w, "-T", "--upload-file"):
			return "curl PUT"
		case m == "" && hasFlag(w, "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode", "-F", "--form", "--json"):
			return "curl POST"
		}
	case "wget":
		if m := strings.ToUpper(flagValue(w, "--method")); writeMethods[m] {
			return "wget " + m
		}
		if hasFlag(w, "--post-data", "--post-file") {
			return "wget POST"
		}
	case "http", "https", "xh", "xhs":
		if m := strings.ToUpper(first(args(w), 0)); writeMethods[m] {
			return prog + " " + m
		}
	case "npm", "pnpm", "yarn", "bun":
		a := args(w)
		switch v := first(a, 0); {
		case oneOf(v, "publish", "unpublish", "deprecate", "dist-tag"):
			return prog + " " + v
		case oneOf(v, "install", "i", "add", "uninstall", "remove", "rm") && hasFlag(w, "-g", "--global"):
			return prog + " " + v + " -g"
		}
	case "cargo":
		if v := first(args(w), 0); oneOf(v, "publish", "yank", "install", "uninstall", "owner") {
			return "cargo " + v
		}
	case "go":
		if first(args(w), 0) == "install" {
			return "go install"
		}
	case "pipx":
		if v := first(args(w), 0); oneOf(v, "install", "uninstall", "upgrade", "reinstall") {
			return "pipx " + v
		}
	case "twine", "poetry", "uv", "hatch", "flit":
		a := args(w)
		if v := first(a, 0); oneOf(v, "upload", "publish") {
			return prog + " " + v
		}
		if prog == "uv" && first(a, 0) == "tool" && oneOf(first(a, 1), "install", "uninstall", "upgrade") {
			return "uv tool " + first(a, 1)
		}
	case "gem":
		if v := first(args(w), 0); oneOf(v, "push", "yank", "owner") {
			return "gem " + v
		}
	case "docker", "podman":
		a := args(w, "-H", "--host", "--context")
		switch v := first(a, 0); {
		case oneOf(v, "push", "rm", "rmi", "stop", "kill", "restart"):
			return prog + " " + v
		case oneOf(v, "compose") && oneOf(first(a, 1), "up", "down", "stop", "restart", "rm", "push"):
			return prog + " compose " + first(a, 1)
		case oneOf(v, "image", "container", "volume", "network", "system") && oneOf(first(a, 1), "push", "rm", "prune", "stop", "kill", "create"):
			return prog + " " + v + " " + first(a, 1)
		}
	case "docker-compose":
		if v := first(args(w), 0); oneOf(v, "up", "down", "stop", "restart", "rm", "push") {
			return "docker-compose " + v
		}
	case "kubectl", "oc":
		a := args(w, "-n", "--namespace", "--context", "-f", "--filename", "-l", "--selector", "-o", "--output", "-c", "--container", "--kubeconfig")
		if v := first(a, 0); oneOf(v, "apply", "create", "delete", "patch", "replace", "scale", "rollout", "set", "label", "annotate", "edit", "expose", "autoscale", "drain", "cordon", "uncordon", "taint") {
			if v == "rollout" && !oneOf(first(a, 1), "restart", "undo", "pause", "resume") {
				return ""
			}
			return prog + " " + v
		}
	case "helm":
		if v := first(args(w, "-n", "--namespace", "-f", "--values", "--kube-context", "--set", "--version"), 0); oneOf(v, "install", "upgrade", "uninstall", "delete", "rollback", "push") {
			return "helm " + v
		}
	case "terraform", "tofu", "terragrunt":
		if v := first(args(w, "-chdir", "-var", "-var-file", "-target"), 0); oneOf(v, "apply", "destroy", "import") {
			return prog + " " + v
		}
		if first(args(w), 0) == "state" && oneOf(first(args(w), 1), "rm", "mv", "push", "replace-provider") {
			return prog + " state " + first(args(w), 1)
		}
	case "pulumi":
		if v := first(args(w, "-s", "--stack", "-C", "--cwd"), 0); oneOf(v, "up", "update", "destroy", "import", "refresh") {
			return "pulumi " + v
		}
	case "aws":
		a := args(w, "--profile", "--region", "--output", "--query", "--endpoint-url")
		svc, op := first(a, 0), first(a, 1)
		if svc == "s3" {
			switch {
			case oneOf(op, "rm", "mb", "rb"):
				return "aws s3 " + op
			case oneOf(op, "cp", "mv", "sync") && strings.HasPrefix(a[len(a)-1], "s3://"):
				return "aws s3 " + op
			}
			return ""
		}
		if cloudVerb(op) {
			return "aws " + svc + " " + op
		}
	case "gcloud", "az":
		a := args(w, "--project", "--region", "--zone", "--format", "-g", "--resource-group", "--subscription", "-n", "--name")
		if len(a) == 0 || oneOf(a[0], "config", "auth", "account", "login", "logout") {
			return "" // local settings
		}
		// a[0] is the service group (run, compute, group, ...).
		for _, v := range a[1:] {
			if cloudVerb(v) {
				return prog + " " + strings.Join(a[:min(len(a), 3)], " ")
			}
		}
	case "firebase", "netlify", "wrangler", "fly", "flyctl", "railway", "vercel", "serverless", "sls", "cdk", "amplify", "eb", "heroku", "supabase":
		a := args(w)
		v := first(a, 0)
		if oneOf(v, "deploy", "publish", "up", "push", "destroy", "release") || (prog == "vercel" && v == "") {
			if v == "" {
				v = "deploy"
			}
			return prog + " " + v
		}
		if prog == "supabase" && v == "db" && oneOf(first(a, 1), "push", "reset") {
			return "supabase db " + first(a, 1)
		}
	case "goreleaser":
		if first(args(w), 0) == "release" && !hasFlag(w, "--snapshot") {
			return "goreleaser release"
		}
	case "scp":
		if a := args(w, "-P", "-i", "-o", "-F", "-J"); len(a) >= 2 && remotePath(a[len(a)-1]) {
			return "scp upload"
		}
	case "rsync":
		if a := args(w, "-e", "--rsh", "--exclude", "--include", "--filter", "--port"); len(a) >= 2 && remotePath(a[len(a)-1]) {
			return "rsync upload"
		}
	case "systemctl":
		if v := first(args(w), 0); oneOf(v, "start", "stop", "restart", "reload", "enable", "disable", "mask", "unmask", "kill", "daemon-reload") {
			return "systemctl " + v
		}
	case "service":
		if v := first(args(w), 1); oneOf(v, "start", "stop", "restart", "reload") {
			return "service " + v
		}
	case "launchctl":
		if v := first(args(w), 0); oneOf(v, "load", "unload", "start", "stop", "kickstart", "bootstrap", "bootout", "enable", "disable") {
			return "launchctl " + v
		}
	case "crontab":
		if !hasFlag(w, "-l") {
			return "crontab"
		}
	case "apt", "apt-get", "dnf", "yum", "zypper", "brew", "snap", "flatpak", "winget", "choco", "scoop", "port":
		if v := first(args(w), 0); oneOf(v, "install", "remove", "uninstall", "purge", "upgrade", "reinstall", "autoremove", "dist-upgrade", "full-upgrade", "tap", "untap") {
			return prog + " " + v
		}
	case "pacman":
		for _, f := range w[1:] {
			// -S installs, but -Ss, -Si, -Sl, -Sg and -Sp only look things up.
			if strings.HasPrefix(f, "-R") || strings.HasPrefix(f, "-U") ||
				(strings.HasPrefix(f, "-S") && !strings.ContainsAny(f[2:], "silgp")) {
				return "pacman"
			}
		}
	case "make":
		if oneOf("install", args(w, "-C", "-f", "-j")...) {
			return "make install"
		}
	case "psql", "mysql", "mariadb", "sqlite3", "sqlcmd", "clickhouse-client", "mongosh", "redis-cli":
		up := strings.ToUpper(strings.Join(w[1:], " "))
		for _, k := range []string{"INSERT ", "UPDATE ", "DELETE ", "DROP ", "CREATE ", "ALTER ", "TRUNCATE ", "GRANT ", "REVOKE ", "MERGE ", "UPSERT ",
			".INSERT", ".UPDATE", ".DELETE", ".DROP", " SET ", " DEL ", "FLUSHALL", "FLUSHDB"} {
			if strings.Contains(" "+up+" ", k) {
				return prog + " write"
			}
		}
	}
	return ""
}

// cloudVerb reports whether a cloud CLI operation name changes something:
// create-bucket, delete, deploy, put-object, update-function-code, ...
func cloudVerb(op string) bool {
	verb, _, _ := strings.Cut(op, "-")
	return oneOf(verb, "create", "delete", "deploy", "put", "update", "terminate", "run", "start", "stop", "reboot", "modify",
		"attach", "detach", "associate", "disassociate", "invoke", "publish", "remove", "add", "set", "tag", "untag",
		"enable", "disable", "import", "restore", "replace", "register", "deregister", "patch", "resize", "upload", "send", "purge")
}

// remotePath reports whether an scp/rsync path is on another machine
// (host:path), not a local Windows path like C:\x.
func remotePath(p string) bool {
	i := strings.Index(p, ":")
	if i <= 0 || strings.Contains(p[:i], "/") {
		return false
	}
	return i > 1 || !strings.HasPrefix(p[i+1:], `\`)
}

// mcpReadVerbs start the names of MCP tools that only read.
var mcpReadVerbs = map[string]bool{
	"get": true, "list": true, "search": true, "read": true, "fetch": true, "find": true, "query": true,
	"describe": true, "view": true, "show": true, "check": true, "count": true, "download": true,
	"lookup": true, "browse": true, "inspect": true, "preview": true, "explain": true, "export": true,
	"validate": true, "resolve": true, "retrieve": true, "load": true, "open": true, "watch": true, "poll": true,
	"authenticate": true, "auth": true, "login": true, "complete": true, "guide": true, "help": true, "status": true,
	"navigate": true, "screenshot": true, "summarize": true, "analyze": true, "compare": true, "diff": true,
	"tabs": true, "select": true, "switch": true,
}

// mcpWriteVerbs mark MCP tools that change something.
var mcpWriteVerbs = map[string]bool{
	"create": true, "update": true, "delete": true, "remove": true, "send": true, "post": true, "publish": true,
	"write": true, "set": true, "add": true, "edit": true, "modify": true, "insert": true, "upsert": true,
	"put": true, "patch": true, "move": true, "rename": true, "archive": true, "unarchive": true, "close": true,
	"reopen": true, "merge": true, "submit": true, "upload": true, "deploy": true, "cancel": true, "approve": true,
	"reject": true, "reply": true, "comment": true, "invite": true, "share": true, "unshare": true, "schedule": true,
	"trigger": true, "assign": true, "unassign": true, "transfer": true, "revoke": true, "grant": true,
	"enable": true, "disable": true, "restart": true, "stop": true, "start": true, "pause": true, "resume": true,
	"apply": true, "execute": true, "exec": true, "react": true, "pin": true, "unpin": true, "label": true,
	"star": true, "fork": true, "push": true, "commit": true, "save": true, "store": true, "import": true,
	"mark": true, "drop": true, "truncate": true, "purge": true, "destroy": true,
	"provision": true, "rollback": true, "migrate": true, "book": true, "reserve": true, "pay": true, "charge": true,
	"refund": true, "subscribe": true, "unsubscribe": true, "notify": true, "email": true, "message": true,
}

// mcpAction classifies an MCP tool (mcp__server__tool) by the words in its
// tool name: one that starts with a read verb only reads; otherwise one
// that contains a write verb changes something.
func mcpAction(name string) string {
	rest := strings.TrimPrefix(name, "mcp__")
	server, tool, ok := strings.Cut(rest, "__")
	if !ok || server == "ccd_session" { // Claude Code's own bookkeeping
		return ""
	}
	words := nameWords(tool)
	if len(words) == 0 || mcpReadVerbs[words[0]] {
		return ""
	}
	for _, w := range words {
		if mcpWriteVerbs[w] {
			return mcpServerName(server) + ": " + tool
		}
	}
	return ""
}

// nameWords splits snake_case, kebab-case and camelCase into lower-case words.
func nameWords(s string) []string {
	var out []string
	var b strings.Builder
	prevLower := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush()
			prevLower = false
			continue
		case unicode.IsUpper(r) && prevLower:
			flush()
		}
		b.WriteRune(unicode.ToLower(r))
		prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
	}
	flush()
	return out
}

// mcpServerName shortens an MCP server's name for display:
// claude_ai_Google_Calendar -> Google_Calendar.
func mcpServerName(s string) string {
	return strings.TrimPrefix(s, "claude_ai_")
}
