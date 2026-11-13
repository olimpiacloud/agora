package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/core"
)

func TestForkCountsAndBelongsToTheCaller(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	id, tok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"workspace", "message"}})
	var first string
	for i := 0; i < 5; i++ {
		code, out := e.req(tok, "POST", "/v1/workspaces/"+ws+"/fork", map[string]any{"name": "f"})
		if code != 200 {
			t.Fatalf("fork %d: %d %v", i+1, code, out)
		}
		if out["owner"] != id {
			t.Fatalf("fork %d owner: %v, want %s", i+1, out["owner"], id)
		}
		if first == "" {
			first = out["id"].(string)
		}
	}
	if code, out := e.req(tok, "GET", "/v1/workspaces/"+first+"/board", nil); code != 200 {
		t.Fatalf("board of own fork: %d %v", code, out)
	}
	if code, out := e.req(tok, "POST", "/v1/workspaces/"+ws+"/fork", map[string]any{"name": "f"}); code != 403 {
		t.Fatalf("sixth fork: %d %v, want 403", code, out)
	}
	code, out := e.req(e.admin, "POST", "/v1/workspaces/"+ws+"/fork", map[string]any{"name": "f"})
	if code != 200 {
		t.Fatalf("admin fork: %d %v", code, out)
	}
	if out["owner"] != nil {
		t.Fatalf("admin fork owner: %v", out["owner"])
	}
}

func TestDeletedOwnerWorkspacesStillCount(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, ptok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"workspace", "spawn"}})
	child, ctok := e.cursor(ptok, map[string]any{"permissions": []string{"workspace"}})
	for i := 0; i < 5; i++ {
		if code, out := e.req(ctok, "POST", "/v1/workspaces", map[string]any{}); code != 201 {
			t.Fatalf("workspace %d: %d %v", i+1, code, out)
		}
	}
	if code, out := e.req(ptok, "DELETE", "/v1/cursors/"+child, nil); code != 200 {
		t.Fatalf("delete child: %d %v", code, out)
	}
	if code, out := e.req(ptok, "POST", "/v1/workspaces", map[string]any{}); code != 403 {
		t.Fatalf("workspace after deleting the owner: %d %v, want 403", code, out)
	}
}

func TestCursorCannotDisableIdleSuspend(t *testing.T) {
	e := newEnv(t, func(c *core.Config) { c.SuspendAfter = 30 * time.Minute })
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"workspace"}})
	suspendAfter := func(tok, value string, want int) string {
		t.Helper()
		code, out := e.req(tok, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"suspend_after": value}})
		if code != want {
			t.Fatalf("suspend_after %s: %d %v, want %d", value, code, out, want)
		}
		if code != 201 {
			return ""
		}
		return out["config"].(map[string]any)["suspend_after"].(string)
	}
	if got := suspendAfter(tok, "720h", 201); got != "30m0s" {
		t.Fatalf("cursor suspend_after 720h stored as %s", got)
	}
	if got := suspendAfter(tok, "10m", 201); got != "10m0s" {
		t.Fatalf("cursor suspend_after 10m stored as %s", got)
	}
	suspendAfter(tok, "-1h", 400)
	if got := suspendAfter(e.admin, "720h", 201); got != "720h0m0s" {
		t.Fatalf("admin suspend_after 720h stored as %s", got)
	}
	if code, out := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"ttl": "-1h"}}); code != 400 {
		t.Fatalf("negative ttl: %d %v, want 400", code, out)
	}
}

func TestCursorSnapshotsAreBounded(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"workspace"}})
	for _, c := range []string{"ckpt_a", "ckpt_b", "ckpt_c"} {
		if err := os.MkdirAll(filepath.Join(e.m.Config().Home, "checkpoints", ws, c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := e.req(tok, "POST", "/v1/workspaces/"+ws+"/checkpoint", map[string]any{"snapshot": true}); code != 403 {
		t.Fatalf("fourth snapshot by a cursor: %d %v, want 403", code, out)
	}
	if code, out := e.req(tok, "POST", "/v1/workspaces/"+ws+"/checkpoint", map[string]any{}); code != 200 {
		t.Fatalf("checkpoint without snapshot: %d %v", code, out)
	}
	if code, out := e.req(e.admin, "POST", "/v1/workspaces/"+ws+"/checkpoint", map[string]any{"snapshot": true}); code != 200 {
		t.Fatalf("admin snapshot: %d %v", code, out)
	}
}
