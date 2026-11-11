package api

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCursorWorktreeLimitOverHTTP(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newEnv(t)
	repo := filepath.Join(t.TempDir(), "r")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"process"}})
	var ids []string
	t.Cleanup(func() {
		for _, id := range ids {
			e.req(e.admin, "DELETE", "/v1/worktrees/"+id+"?force=1", nil)
		}
	})
	create := func(tok string, want int) {
		t.Helper()
		code, out := e.req(tok, "POST", "/v1/worktrees", map[string]any{"repo": repo})
		if code != want {
			t.Fatalf("create worktree %d: got %d %v, want %d", len(ids), code, out, want)
		}
		if id, ok := out["id"].(string); ok && code == 201 {
			ids = append(ids, id)
		}
	}
	for i := 0; i < 10; i++ {
		create(tok, 201)
	}
	create(tok, 403)
	create(e.admin, 201)
}
