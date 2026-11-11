package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func (e *env) headlessWorkspace() string {
	code, out := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"name": "h", "config": map[string]any{"headless": true}})
	if code != 201 {
		e.t.Fatalf("create workspace: %d %v", code, out)
	}
	return out["id"].(string)
}

func (e *env) queryStatus(path string) int {
	e.t.Helper()
	resp, err := http.Get(e.srv.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (e *env) cursorIDs(tok string) map[string]bool {
	e.t.Helper()
	r, _ := http.NewRequest("GET", e.srv.URL+"/v1/cursors", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e.t.Fatalf("list cursors: %d", resp.StatusCode)
	}
	var list []map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	out := map[string]bool{}
	for _, c := range list {
		out[c["id"].(string)] = true
	}
	return out
}

func TestChildPermissionsAreBoundedByParent(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, tp := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"browser", "spawn"}})
	if code, out := e.req(tp, "POST", "/v1/cursors", map[string]any{"permissions": []string{"desktop", "process"}}); code != 403 {
		t.Fatalf("child asking only for unheld permissions: got %d %v, want 403", code, out)
	}
	code, out := e.req(tp, "POST", "/v1/cursors", map[string]any{"permissions": []string{"browser", "desktop"}})
	if code != 201 {
		t.Fatalf("child asking for a subset: %d %v", code, out)
	}
	if perms := out["cursor"].(map[string]any)["permissions"].([]any); len(perms) != 1 || perms[0] != "browser" {
		t.Fatalf("child permissions: got %v, want [browser]", perms)
	}
	_, out = e.req(tp, "POST", "/v1/cursors", map[string]any{})
	if perms := out["cursor"].(map[string]any)["permissions"].([]any); len(perms) != 2 {
		t.Fatalf("child without request: got %v, want the parent's", perms)
	}
}

func TestSubagentCannotUseFullProfile(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws})
	if code, _ := e.req(tok, "POST", "/v1/cursors", map[string]any{"goal": "x", "launch": map[string]any{"adapter": "claude", "profile": "full"}}); code != 403 {
		t.Fatalf("full profile by cursor: got %d, want 403", code)
	}
}

func TestDesktopReadsNeedDesktopPermission(t *testing.T) {
	e := newEnv(t)
	ws := e.headlessWorkspace()
	_, browserOnly := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"browser"}})
	_, desk := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"desktop"}})
	reads := []struct{ method, path string }{
		{"GET", "/v1/workspaces/" + ws + "/clipboard"},
		{"POST", "/v1/workspaces/" + ws + "/clipboard"},
		{"GET", "/v1/workspaces/" + ws + "/screenshot"},
		{"GET", "/v1/workspaces/" + ws + "/windows"},
	}
	for _, rd := range reads {
		if code, _ := e.req(browserOnly, rd.method, rd.path, nil); code != 403 {
			t.Fatalf("%s %s without desktop: got %d, want 403", rd.method, rd.path, code)
		}
		if code, _ := e.req(desk, rd.method, rd.path, nil); code == 403 || code == 401 {
			t.Fatalf("%s %s with desktop: got %d", rd.method, rd.path, code)
		}
	}
}

func TestPrivilegedWorkspaceOpsNeedWorkspacePermission(t *testing.T) {
	e := newEnv(t)
	ws := e.headlessWorkspace()
	_, plain := e.cursor(e.admin, map[string]any{"workspace": ws})
	_, wsPerm := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"workspace"}})
	for _, op := range []string{"thaw", "browser-restart"} {
		if code, _ := e.req(plain, "POST", "/v1/workspaces/"+ws+"/"+op, nil); code != 403 {
			t.Fatalf("%s without workspace permission: got %d, want 403", op, code)
		}
	}
	if code, out := e.req(wsPerm, "POST", "/v1/workspaces/"+ws+"/thaw", nil); code != 200 {
		t.Fatalf("thaw with workspace permission: %d %v", code, out)
	}
}

func TestKillProcessNeedsPermissionAndOwnership(t *testing.T) {
	e := newEnv(t)
	ws := e.headlessWorkspace()
	_, tp := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"process", "spawn"}})
	_, tc := e.cursor(tp, map[string]any{"permissions": []string{"process"}})
	_, stranger := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"process"}})
	_, manager := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"process", "workspace"}})
	_, browserOnly := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"browser"}})
	launch := func(tok string) string {
		t.Helper()
		code, out := e.req(tok, "POST", "/v1/workspaces/"+ws+"/apps", map[string]any{"command": "sleep", "args": []string{"30"}})
		if code != 201 {
			t.Fatalf("launch: %d %v", code, out)
		}
		return "/v1/workspaces/" + ws + "/processes/" + strconv.Itoa(int(out["pid"].(float64)))
	}
	kill := func(tok, path string, want int) {
		t.Helper()
		if code, out := e.req(tok, "DELETE", path, nil); code != want {
			t.Fatalf("kill %s: got %d %v, want %d", path, code, out, want)
		}
	}
	child := launch(tc)
	kill(browserOnly, child, 403)
	kill(stranger, child, 403)
	kill(tp, child, 200)
	other := launch(tc)
	kill(manager, other, 200)
	admins := launch(e.admin)
	kill(tc, admins, 403)
	kill(e.admin, admins, 200)
}

func TestWorkspacelessCursorsDoNotSeeEachOther(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	a, ta := e.cursor(e.admin, map[string]any{})
	b, _ := e.cursor(e.admin, map[string]any{})
	_, tw := e.cursor(e.admin, map[string]any{"workspace": ws})
	sib, _ := e.cursor(e.admin, map[string]any{"workspace": ws})
	if got := e.cursorIDs(ta); got[b] || !got[a] {
		t.Fatalf("workspace-less cursor listing: %v", got)
	}
	if got := e.cursorIDs(tw); !got[sib] || got[a] {
		t.Fatalf("workspace cursor listing: %v", got)
	}
}

func TestSpawnWithWorktreeNeedsProcessPermission(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	repo := t.TempDir()
	_, spawnOnly := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"spawn"}})
	_, withProcess := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"spawn", "process"}})
	if code, _ := e.req(spawnOnly, "POST", "/v1/cursors", map[string]any{"worktree": map[string]any{"repo": repo}}); code != 403 {
		t.Fatalf("worktree without process permission: got %d, want 403", code)
	}
	if code, _ := e.req(withProcess, "POST", "/v1/cursors", map[string]any{"worktree": map[string]any{"repo": repo}}); code == 403 {
		t.Fatal("worktree with process permission: got 403")
	}
}

func TestQueryTokenMustBeViewerToken(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws})
	if code := e.queryStatus("/v1/events/stream?token=" + tok); code != 401 {
		t.Fatalf("cursor token in query: got %d, want 401", code)
	}
	if code := e.queryStatus("/v1/events/stream?token=" + e.admin); code != 401 {
		t.Fatalf("admin token in query: got %d, want 401", code)
	}
	_, out := e.req(e.admin, "POST", "/v1/viewer-tokens", map[string]any{"workspace": ws})
	if code := e.queryStatus("/v1/events/stream?token=" + out["token"].(string)); code != 200 {
		t.Fatalf("viewer token in query: got %d, want 200", code)
	}
}
