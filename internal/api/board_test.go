package api

import (
	"strconv"
	"testing"
)

func TestBoardClaimsAndScope(t *testing.T) {
	e := newEnv(t)
	wa, wb := e.workspace(), e.workspace()
	_, tp := e.cursor(e.admin, map[string]any{"workspace": wa})
	_, t1 := e.cursor(tp, map[string]any{"goal": "a"})
	c2, t2 := e.cursor(tp, map[string]any{"goal": "b"})
	_, tx := e.cursor(e.admin, map[string]any{"workspace": wb})
	path := "/v1/workspaces/" + wa + "/board"

	if code, _ := e.req(t1, "POST", path, map[string]any{"kind": "claim", "topic": "Audit  Sidebar"}); code != 201 {
		t.Fatalf("first claim: %d", code)
	}
	code, out := e.req(t2, "POST", path, map[string]any{"kind": "claim", "topic": "audit sidebar"})
	if code != 409 || out["claim"] == nil {
		t.Fatalf("second claim: got %d %v, want 409 with claim", code, out)
	}
	if code, _ := e.req(t1, "POST", path, map[string]any{"kind": "done", "topic": "audit sidebar", "text": "3 issues"}); code != 201 {
		t.Fatalf("done: %d", code)
	}
	if code, _ := e.req(t2, "POST", path, map[string]any{"kind": "claim", "topic": "audit sidebar"}); code != 409 {
		t.Fatalf("claim of finished topic: got %d, want 409", code)
	}
	if code, _ := e.req(t2, "POST", path, map[string]any{"kind": "claim", "topic": "audit header"}); code != 201 {
		t.Fatalf("claim other topic: %d", code)
	}
	if code, _ := e.req(t1, "POST", path, map[string]any{"kind": "release", "topic": "audit header"}); code != 400 {
		t.Fatalf("release of someone else's claim: got %d, want 400", code)
	}
	if code, _ := e.req(e.admin, "POST", "/v1/cursors/"+c2+"/cancel", nil); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	if code, _ := e.req(t1, "POST", path, map[string]any{"kind": "claim", "topic": "audit header"}); code != 201 {
		t.Fatalf("claim after holder ended: got %d, want 201", code)
	}

	code, out = e.req(tp, "GET", path+"?since=0", nil)
	if code != 200 {
		t.Fatalf("parent read: %d", code)
	}
	if n := len(out["posts"].([]any)); n != 4 {
		t.Fatalf("posts: got %d, want 4", n)
	}
	if n := len(out["claims"].([]any)); n != 2 {
		t.Fatalf("claims: got %d, want 2", n)
	}
	seq := int64(out["seq"].(float64))
	_, out = e.req(t1, "GET", path+"?since="+strconv.FormatInt(seq, 10), nil)
	if n := len(out["posts"].([]any)); n != 0 {
		t.Fatalf("posts after seq: got %d, want 0", n)
	}
	if code, _ := e.req(tx, "GET", path, nil); code != 403 {
		t.Fatalf("foreign read: got %d, want 403", code)
	}
	if code, _ := e.req(tx, "POST", path, map[string]any{"kind": "note", "text": "x"}); code != 403 {
		t.Fatalf("foreign post: got %d, want 403", code)
	}
}
