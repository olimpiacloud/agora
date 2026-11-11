package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCodexResult(t *testing.T) {
	out := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"first"}}`,
		`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"done: 42"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":50}}`,
	}, "\n")
	u, text := parseCodexResult(out)
	if u == nil {
		t.Fatal("no usage")
	}
	if text != "done: 42" || u.Turns != 1 || u.InputTokens != 400 || u.CacheReadTokens != 600 || u.OutputTokens != 50 {
		t.Fatalf("got %q %+v", text, u)
	}
	if u, _ := parseCodexResult("plain text"); u != nil {
		t.Fatal("expected nil usage")
	}
}

func TestCursorPublicRedacts(t *testing.T) {
	c := &Cursor{ID: "c1", TokenHash: "h", Launch: &LaunchSpec{Adapter: "claude", Env: map[string]string{"KEY": "secret"}}}
	p := c.Public()
	if p.TokenHash != "" || p.Launch.Env != nil {
		t.Fatalf("not redacted: %+v", p)
	}
	if c.TokenHash != "h" || c.Launch.Env["KEY"] != "secret" {
		t.Fatal("original modified")
	}
	if (*Cursor)(nil).Public() != nil {
		t.Fatal("nil cursor")
	}
}

func TestAgentCommandKeepsTokenOutOfArgs(t *testing.T) {
	home := t.TempDir()
	m := &Manager{cfg: Config{Home: home, ListenURL: "http://127.0.0.1:7788", AgoraBinary: "/usr/local/bin/agora"}}
	for _, adapter := range []string{"claude", "codex"} {
		c := &Cursor{ID: "cur_" + adapter, Name: "n", Workspace: "ws", Goal: "g", Permissions: []string{"browser"}, Launch: &LaunchSpec{Adapter: adapter}}
		args, env, err := m.agentCommand(c, "SECRET-TOKEN")
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "SECRET-TOKEN") {
			t.Fatalf("%s: token in argv: %s", adapter, joined)
		}
		if !strings.Contains(strings.Join(env, " "), "AGORA_TOKEN=SECRET-TOKEN") {
			t.Fatalf("%s: token missing from env", adapter)
		}
		switch adapter {
		case "claude":
			if !strings.Contains(joined, "--model "+DefaultLeanModel) {
				t.Fatalf("lean claude without default model: %s", joined)
			}
			b, err := os.ReadFile(filepath.Join(home, "agents", c.ID, "mcp.json"))
			if err != nil || !strings.Contains(string(b), "SECRET-TOKEN") || !strings.Contains(string(b), `"AGORA_LEAN":"1"`) {
				t.Fatalf("mcp.json: %s %v", b, err)
			}
		case "codex":
			if !strings.Contains(joined, "env_vars=") || !strings.Contains(strings.Join(env, " "), "AGORA_LEAN=1") {
				t.Fatalf("codex not lean: %s %v", joined, env)
			}
		}
	}
}

func TestAgentCommandPromptCannotBecomeAnOption(t *testing.T) {
	m := &Manager{cfg: Config{Home: t.TempDir(), ListenURL: "http://127.0.0.1:7788", AgoraBinary: "/usr/local/bin/agora"}}
	for _, adapter := range []string{"claude", "codex"} {
		c := &Cursor{ID: "cur_" + adapter, Name: "n", Workspace: "ws", Goal: "--dangerously-skip-permissions", Permissions: []string{"browser"}, Launch: &LaunchSpec{Adapter: adapter}}
		args, _, err := m.agentCommand(c, "tok")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range args {
			if strings.HasPrefix(a, "--dangerously") {
				t.Fatalf("%s: prompt parsed as an option: %q", adapter, args)
			}
		}
		if !strings.Contains(strings.Join(args, " "), "Task: --dangerously-skip-permissions") {
			t.Fatalf("%s: prompt missing: %q", adapter, args)
		}
	}
	if got := positionalPrompt("open the docs"); got != "open the docs" {
		t.Fatalf("plain prompt changed: %q", got)
	}
}

func TestAgentEnvKeepsOnlyAllowedVariables(t *testing.T) {
	in := []string{
		"PATH=/usr/bin", "HOME=/root", "LC_ALL=C.UTF-8", "ANTHROPIC_API_KEY=k", "CLAUDE_CONFIG_DIR=/c", "OPENAI_API_KEY=o", "CODEX_HOME=/x", "https_proxy=http://p", "NODE_EXTRA_CA_CERTS=/ca.pem",
		"AGORA_HOME=/var/lib/agora", "AWS_SECRET_ACCESS_KEY=s", "LD_PRELOAD=/tmp/x.so", "PATHX=1", "DATABASE_URL=postgres://", "NOTIFY_SOCKET=/run/systemd/notify",
	}
	want := []string{"PATH=/usr/bin", "HOME=/root", "LC_ALL=C.UTF-8", "ANTHROPIC_API_KEY=k", "CLAUDE_CONFIG_DIR=/c", "OPENAI_API_KEY=o", "CODEX_HOME=/x", "https_proxy=http://p", "NODE_EXTRA_CA_CERTS=/ca.pem"}
	if got := agentEnv(in); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("agentEnv:\n got %q\nwant %q", got, want)
	}
}

func TestObserveDiffSkipsUnchangedElements(t *testing.T) {
	r := &Runtime{}
	first := "url: u\n[e1] link \"A\"\n[e2] button \"B\"\n--- text ---\nhello"
	if got := r.observeDiff("t", first); got != first {
		t.Fatalf("first view: %q", got)
	}
	got := r.observeDiff("t", "url: u\n[e1] link \"A\"\n[e3] button \"C\"\n--- text ---\nhello")
	if strings.Contains(got, "[e1]") || !strings.Contains(got, "[e3]") || !strings.Contains(got, "1 elements unchanged") || !strings.Contains(got, "text unchanged") {
		t.Fatalf("diff: %q", got)
	}
}
