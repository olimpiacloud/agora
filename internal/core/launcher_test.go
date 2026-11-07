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
