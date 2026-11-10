package activity

import (
	"testing"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

func TestActionKind(t *testing.T) {
	bash := map[string]string{
		"git push":                                "git push",
		"cd repo && git push -q origin main":      "git push",
		"git -C /x push origin v1":                "git push",
		"git push --dry-run":                      "",
		"git status && git log":                   "",
		"gh pr create --title x --body y":         "gh pr create",
		"gh pr view 12":                           "",
		"gh pr list":                              "",
		"gh release create v1.0":                  "gh release create",
		"gh run list --limit 3":                   "",
		"gh api repos/x/y":                        "",
		"gh api -X DELETE repos/x/y":              "gh api DELETE",
		"gh api repos/x/y/issues -f title=t":      "gh api POST",
		"curl -s https://x.dev/api":               "",
		"curl -X POST https://x.dev/api":          "curl POST",
		"curl -XDELETE https://x.dev/a/1":         "curl DELETE",
		"curl -d '{\"a\":1}' https://x.dev":       "curl POST",
		"curl --request=patch https://x.dev":      "curl PATCH",
		"curl -s 'https://x.dev?q=a;b' | jq .":    "",
		"http POST x.dev/api a=1":                 "http POST",
		"npm publish":                             "npm publish",
		"npm install":                             "",
		"npm install -g typescript":               "npm install -g",
		"sudo -u root apt-get install -y jq":      "apt-get install",
		"FOO=1 BAR=2 docker push img:1":           "docker push",
		"docker ps":                               "",
		"kubectl get pods":                        "",
		"kubectl -n prod apply -f x.yaml":         "kubectl apply",
		"kubectl rollout status deploy/x":         "",
		"kubectl rollout restart deploy/x":        "kubectl rollout",
		"terraform plan":                          "",
		"terraform apply -auto-approve":           "terraform apply",
		"aws s3 ls s3://b":                        "",
		"aws s3 cp s3://b/x .":                    "",
		"aws s3 cp x s3://b/x":                    "aws s3 cp",
		"aws lambda update-function-code --zip x": "aws lambda update-function-code",
		"aws ec2 describe-instances":              "",
		"gcloud run deploy svc --image x":         "gcloud run deploy svc",
		"gcloud run services list":                "",
		"gcloud config set project p":             "",
		"systemctl status x":                      "",
		"sudo systemctl restart nginx":            "systemctl restart",
		"pacman -Ss jq":                           "",
		"pacman -Syu":                             "pacman",
		"rsync -av dist/ host:/srv/www":           "rsync upload",
		"rsync -av src/ dst/":                     "",
		"scp file.txt user@host:~":                "scp upload",
		"make install 2>&1 | tail -3":             "make install",
		"make test":                               "",
		"go install ./cmd/x":                      "go install",
		"psql -c 'select 1'":                      "",
		"psql -c \"update users set a=1\"":        "psql write",
		"pkill -f server":                         "",
		"echo 'git push' > notes.txt":             "",
		"timeout 30 git push":                     "git push",
		"goreleaser release --snapshot --clean":   "",
		"vercel --prod":                           "vercel deploy",
		"ls | xargs -I{} kubectl delete pod {}":   "kubectl delete",
		"crontab -l":                              "",
		"/usr/bin/git push":                       "git push",
		"grep -rn 'curl -X POST' .":               "",
	}
	for cmd, want := range bash {
		if got := ActionKind(transcript.ToolUse{Name: "Bash", Command: cmd}); got != want {
			t.Errorf("%q = %q, want %q", cmd, got, want)
		}
	}
	mcp := map[string]string{
		"mcp__claude_ai_Gmail__send_email":             "Gmail: send_email",
		"mcp__claude_ai_Gmail__list_messages":          "",
		"mcp__github__create_pull_request":             "github: create_pull_request",
		"mcp__github__get_pull_request":                "",
		"mcp__slack__postMessage":                      "slack: postMessage",
		"mcp__linear__searchIssues":                    "",
		"mcp__claude_ai_Claude_Docs__update":           "Claude_Docs: update",
		"mcp__claude-in-chrome__browser_batch":         "",
		"mcp__ccd_session__mark_chapter":               "",
		"mcp__claude_ai_Google_Calendar__authenticate": "",
		"mcp__notion__notion-update-page":              "notion: notion-update-page",
		"mcp__x__thing":                                "",
		"Read":                                         "",
		"WebFetch":                                     "",
	}
	for name, want := range mcp {
		if got := ActionKind(transcript.ToolUse{Name: name}); got != want {
			t.Errorf("%q = %q, want %q", name, got, want)
		}
	}
}

func TestEngineRecordsActions(t *testing.T) {
	e := New()
	src := transcript.Source{SessionID: "s1"}
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	e.Observe(transcript.Record{Kind: transcript.KindUser, SessionID: "s1", Timestamp: at, Prompt: "ship it"}, src)
	e.Observe(transcript.Record{Kind: transcript.KindAssistant, SessionID: "s1", Timestamp: at.Add(time.Minute), MessageID: "m1",
		ToolUses: []transcript.ToolUse{
			{ID: "t1", Name: "Bash", Command: "git push origin main", Detail: "Push to GitHub"},
			{ID: "t2", Name: "Bash", Command: "go test ./..."},
			{ID: "t3", Name: "mcp__github__create_issue"},
		}}, src)
	e.Observe(transcript.Record{Kind: transcript.KindUser, SessionID: "s1", ToolResults: []transcript.ToolResult{
		{ToolUseID: "t1"}, {ToolUseID: "t3", IsError: true, Text: "403 forbidden"}}}, src)
	// A subagent deploys, later.
	e.Observe(transcript.Record{Kind: transcript.KindAssistant, SessionID: "s1", Sidechain: true, Timestamp: at.Add(2 * time.Minute), MessageID: "m2",
		ToolUses: []transcript.ToolUse{{ID: "t4", Name: "Bash", Command: "firebase deploy"}}}, transcript.Source{SessionID: "s1", IsSubagent: true})

	acts := e.Session("s1").Actions
	if len(acts) != 3 {
		t.Fatalf("actions: %+v", acts)
	}
	if a := acts[0]; a.Kind != "firebase deploy" || !a.Subagent || a.Done {
		t.Errorf("newest: %+v", a)
	}
	if a := acts[2]; a.Kind != "git push" || a.Summary != "Push to GitHub" || !a.Done || a.Fail {
		t.Errorf("push: %+v", a)
	}
	if a := acts[1]; a.Kind != "github: create_issue" || !a.Fail || a.Error == "" {
		t.Errorf("issue: %+v", a)
	}
}
