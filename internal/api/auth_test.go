package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/olimpiacloud/agora/internal/core"
	"github.com/olimpiacloud/agora/internal/stream"
)

type env struct {
	t     *testing.T
	m     *core.Manager
	srv   *httptest.Server
	admin string
}

func newEnv(t *testing.T) *env {
	home := t.TempDir()
	m, err := core.NewManager(core.Config{Home: home, RTC: stream.RTCOptions{IncludeLoop: true}})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuth(home, m)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(m, auth, nil))
	t.Cleanup(func() {
		srv.Close()
		m.Shutdown()
	})
	tok, _ := os.ReadFile(filepath.Join(home, "admin.token"))
	return &env{t: t, m: m, srv: srv, admin: strings.TrimSpace(string(tok))}
}

func (e *env) req(tok, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	b, _ := json.Marshal(body)
	rd = bytes.NewReader(b)
	r, _ := http.NewRequest(method, e.srv.URL+path, rd)
	r.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *env) workspace() string {
	code, out := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"name": "w"})
	if code != 201 {
		e.t.Fatalf("create workspace: %d %v", code, out)
	}
	return out["id"].(string)
}

func (e *env) cursor(tok string, body map[string]any) (string, string) {
	code, out := e.req(tok, "POST", "/v1/cursors", body)
	if code != 201 {
		e.t.Fatalf("create cursor: %d %v", code, out)
	}
	return out["cursor"].(map[string]any)["id"].(string), out["token"].(string)
}

func TestMessageStaysInsideWorkspaceAndTree(t *testing.T) {
	e := newEnv(t)
	wa, wb := e.workspace(), e.workspace()
	_, ta := e.cursor(e.admin, map[string]any{"workspace": wa})
	sib, _ := e.cursor(e.admin, map[string]any{"workspace": wa})
	other, _ := e.cursor(e.admin, map[string]any{"workspace": wb})
	if code, _ := e.req(ta, "POST", "/v1/cursors/"+other+"/message", map[string]any{"body": "hi"}); code != 403 {
		t.Fatalf("cross-workspace message: got %d, want 403", code)
	}
	if code, _ := e.req(ta, "POST", "/v1/cursors/"+sib+"/message", map[string]any{"body": "hi"}); code != 200 {
		t.Fatalf("same-workspace message: got %d, want 200", code)
	}
}

func TestTransferCannotEscapeWorkspaceOrTree(t *testing.T) {
	e := newEnv(t)
	wa, wb := e.workspace(), e.workspace()
	pa, ta := e.cursor(e.admin, map[string]any{"workspace": wa})
	child, _ := e.cursor(ta, map[string]any{"goal": "x"})
	stranger, _ := e.cursor(e.admin, map[string]any{"workspace": wa})
	if code, _ := e.req(ta, "POST", "/v1/cursors/"+child+"/transfer", map[string]any{"workspace": wb}); code != 403 {
		t.Fatalf("transfer to foreign workspace: got %d, want 403", code)
	}
	if code, _ := e.req(ta, "POST", "/v1/cursors/"+child+"/transfer", map[string]any{"parent": stranger}); code != 403 {
		t.Fatalf("reparent under stranger: got %d, want 403", code)
	}
	if code, _ := e.req(e.admin, "POST", "/v1/cursors/"+pa+"/transfer", map[string]any{"parent": child}); code == 200 {
		t.Fatal("reparenting a cursor under its own child must fail")
	}
	if code, _ := e.req(e.admin, "POST", "/v1/cursors/"+pa+"/cancel", nil); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
}

func TestSubagentCannotLaunchRawCommands(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws})
	code, _ := e.req(tok, "POST", "/v1/cursors", map[string]any{"goal": "x", "launch": map[string]any{"adapter": "command", "command": []string{"sh", "-c", "id"}}})
	if code != 403 {
		t.Fatalf("command launch by cursor: got %d, want 403", code)
	}
	code, _ = e.req(tok, "POST", "/v1/cursors", map[string]any{"goal": "x", "launch": map[string]any{"adapter": "claude", "env": map[string]string{"LD_PRELOAD": "/tmp/x.so"}}})
	if code != 403 {
		t.Fatalf("launch env by cursor: got %d, want 403", code)
	}
}

func TestCursorCannotInjectBrowserArgsOrProfile(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"workspace", "spawn"}})
	if code, _ := e.req(tok, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"browser_args": []string{"--remote-debugging-port=9222"}}}); code != 403 {
		t.Fatalf("browser_args by cursor: got %d, want 403", code)
	}
	if code, _ := e.req(tok, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"profile": ws}}); code != 403 {
		t.Fatalf("foreign profile by cursor: got %d, want 403", code)
	}
	if code, _ := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"profile": "../state"}}); code != 400 {
		t.Fatalf("path traversal profile: got %d, want 400", code)
	}
	if code, _ := e.req(tok, "POST", "/v1/workspaces", map[string]any{"name": "mine"}); code != 201 {
		t.Fatalf("plain workspace by cursor: got %d, want 201", code)
	}
}

func TestCursorCannotSpawnIntoForeignWorkspace(t *testing.T) {
	e := newEnv(t)
	wa, wb := e.workspace(), e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": wa, "permissions": []string{"workspace", "spawn"}})
	if code, _ := e.req(tok, "POST", "/v1/cursors", map[string]any{"workspace": wb}); code != 403 {
		t.Fatalf("spawn into foreign workspace: got %d, want 403", code)
	}
	_, out := e.req(tok, "POST", "/v1/workspaces", map[string]any{"name": "mine"})
	if code, _ := e.req(tok, "POST", "/v1/cursors", map[string]any{"workspace": out["id"]}); code != 201 {
		t.Fatalf("spawn into own workspace: got %d, want 201", code)
	}
}

func TestEventsOfWorkspacelessCursorAreScoped(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{})
	e.m.Bus.Publish("secret.event", ws, "", nil)
	code, out := e.req(tok, "GET", "/v1/events?types=secret.event", nil)
	if code != 200 {
		t.Fatalf("events: %d", code)
	}
	if evs, _ := out["events"].([]any); len(evs) != 0 {
		t.Fatalf("workspace-less cursor saw %d foreign events", len(evs))
	}
	if code, _ := e.req(tok, "GET", "/v1/events?workspace="+ws, nil); code != 403 {
		t.Fatalf("explicit foreign workspace: got %d, want 403", code)
	}
}

func TestViewerTokenScope(t *testing.T) {
	e := newEnv(t)
	wa, wb := e.workspace(), e.workspace()
	_, out := e.req(e.admin, "POST", "/v1/viewer-tokens", map[string]any{"workspace": wa})
	vt := out["token"].(string)
	if code, _ := e.req(vt, "GET", "/v1/workspaces/"+wa, nil); code != 403 {
		t.Fatalf("viewer token on REST: got %d, want 403", code)
	}
	if code, _ := e.req(vt, "GET", "/v1/events/stream?workspace="+wb, nil); code != 403 {
		t.Fatalf("viewer token on foreign events: got %d, want 403", code)
	}
	bad := vt[:len(vt)-2] + "xx"
	if code, _ := e.req(bad, "GET", "/v1/events/stream", nil); code != 401 {
		t.Fatalf("tampered viewer token: got %d, want 401", code)
	}
}

func TestWorkspaceCursorLimitHoldsUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	_, out := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"budget": map[string]any{"max_cursors": 3}}})
	ws := out["id"].(string)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.req(e.admin, "POST", "/v1/cursors", map[string]any{"workspace": ws})
		}()
	}
	wg.Wait()
	if n := len(e.m.ListCursors(core.CursorQuery{Workspace: ws})); n != 3 {
		t.Fatalf("workspace has %d cursors, limit is 3", n)
	}
}
