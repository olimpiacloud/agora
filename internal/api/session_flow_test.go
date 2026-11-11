package api

import (
	"fmt"
	"testing"
)

func TestSessionFlowOnTodaysAPI(t *testing.T) {
	e := newEnv(t)
	standing, parent := e.cursor(e.admin, map[string]any{"name": "standing", "permissions": []string{"desktop", "browser", "process", "spawn", "message", "workspace"}})

	code, out := e.req(parent, "POST", "/v1/workspaces", map[string]any{"name": "session"})
	if code != 201 {
		t.Fatalf("workspace-less cursor creates a workspace: %d %v", code, out)
	}
	ws := out["id"].(string)
	if out["owner"] != standing {
		t.Fatalf("workspace owner: got %v, want %s", out["owner"], standing)
	}

	code, out = e.req(parent, "POST", "/v1/cursors", map[string]any{"workspace": ws, "name": "session"})
	if code != 201 {
		t.Fatalf("workspace-less cursor places a child in its workspace: %d %v", code, out)
	}
	child := out["token"].(string)
	if c := out["cursor"].(map[string]any); c["workspace"] != ws || c["parent"] != standing {
		t.Fatalf("child cursor: %v", c)
	}

	code, out = e.req(child, "GET", "/v1/me", nil)
	if code != 200 || out["admin"] != false || out["cursor"].(map[string]any)["workspace"] != ws {
		t.Fatalf("child /v1/me: %d %v", code, out)
	}
	if code, out := e.req(child, "GET", "/v1/workspaces/"+ws, nil); code != 200 {
		t.Fatalf("child reads its workspace: %d %v", code, out)
	}
	if code, list := e.reqList(child, "/v1/workspaces"); code != 200 || len(list) != 1 || list[0]["id"] != ws {
		t.Fatalf("child lists its workspace: %d %v", code, list)
	}

	if code, out := e.req(child, "POST", "/v1/cursors", map[string]any{"name": "x", "launch": map[string]any{"adapter": "command", "command": []string{"true"}}}); code != 403 {
		t.Fatalf("child launches a command: got %d %v, want 403", code, out)
	}
	if code, out := e.req(child, "POST", "/v1/cursors", map[string]any{"name": "x", "launch": map[string]any{"adapter": "codex"}}); code != 403 {
		t.Fatalf("child launches codex: got %d %v, want 403", code, out)
	}
	if code, out := e.req(child, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"browser_args": []string{"--x"}}}); code != 403 {
		t.Fatalf("child sets browser_args: got %d %v, want 403", code, out)
	}
	if code, out := e.req(child, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"profile": "default"}}); code != 403 {
		t.Fatalf("child reuses a named profile: got %d %v, want 403", code, out)
	}
	if code, out := e.req(child, "DELETE", "/v1/workspaces/"+ws, nil); code != 403 {
		t.Fatalf("child deletes its workspace: got %d %v, want 403", code, out)
	}
	if code, out := e.req(child, "POST", "/v1/viewer-tokens", map[string]any{"workspace": ws}); code != 403 {
		t.Fatalf("child mints a viewer link: got %d %v, want 403", code, out)
	}
	if code, out := e.req(child, "POST", "/v1/workspaces/"+ws+"/control-take", nil); code != 403 {
		t.Fatalf("child takes human control: got %d %v, want 403", code, out)
	}

	if code, list := e.reqList(parent, "/v1/workspaces"); code != 200 || len(list) != 0 {
		t.Fatalf("gap changed: an owner without a workspace of its own used to see none of its workspaces in GET /v1/workspaces, got %d %v", code, list)
	}
	if code, _ := e.req(parent, "GET", "/v1/workspaces/"+ws, nil); code != 403 {
		t.Fatalf("gap changed: an owner without a workspace of its own used to get 403 reading its workspace, got %d", code)
	}
	if code, _ := e.req(parent, "POST", "/v1/workspaces/"+ws+"/start", nil); code != 403 {
		t.Fatalf("gap changed: an owner without a workspace of its own used to get 403 starting its workspace, got %d", code)
	}
	if code, _ := e.req(parent, "POST", "/v1/workspaces/"+ws+"/tabs", map[string]any{"url": "about:blank"}); code != 403 {
		t.Fatalf("gap changed: an owner without a workspace of its own used to get 403 opening a tab in its workspace, got %d", code)
	}
	if code, out := e.req(parent, "GET", "/v1/workspaces/"+ws+"/board", nil); code != 200 {
		t.Fatalf("owner reads the board of its workspace: %d %v", code, out)
	}

	if code, out := e.req(parent, "GET", "/v1/cursors/self", nil); code != 200 || out["status"] != "pending" {
		t.Fatalf("gap changed: a standing cursor that never acts used to stay pending, the state reapCursors cancels after 24h; got %d %v", code, out)
	}
	code, out = e.req(parent, "POST", "/v1/cursors", map[string]any{"name": "fake", "labels": map[string]any{"paseo_agent": "x"}})
	if code != 201 || out["cursor"].(map[string]any)["labels"].(map[string]any)["paseo_agent"] != "x" {
		t.Fatalf("gap changed: any cursor used to be able to give a child the paseo_agent label that exempts it from reapCursors; got %d %v", code, out)
	}

	for i := 2; i <= 5; i++ {
		if code, out := e.req(parent, "POST", "/v1/workspaces", map[string]any{"name": fmt.Sprintf("session %d", i)}); code != 201 {
			t.Fatalf("session workspace %d: %d %v", i, code, out)
		}
	}
	if code, _ := e.req(parent, "POST", "/v1/workspaces", map[string]any{"name": "session 6"}); code != 403 {
		t.Fatalf("gap changed: a cursor tree used to be capped at 5 live workspaces, so a sixth one-workspace-per-session session got 403; got %d", code)
	}
}
