package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/olimpiacloud/agora/internal/client"
)

type sessionHit struct {
	method, path, auth string
}

type sessionAPI struct {
	mu       sync.Mutex
	hits     []sessionHit
	failWS   bool
	identity map[string]map[string]any
}

func (a *sessionAPI) log() []sessionHit {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sessionHit(nil), a.hits...)
}

func (a *sessionAPI) count(method, path string) int {
	n := 0
	for _, h := range a.log() {
		if h.method == method && h.path == path {
			n++
		}
	}
	return n
}

func newSessionServer(t *testing.T, token string, failWS bool) (*Server, *sessionAPI) {
	api := &sessionAPI{failWS: failWS, identity: map[string]map[string]any{
		"Bearer admin":     {"admin": true, "cursor": nil},
		"Bearer standing":  {"admin": false, "cursor": map[string]any{"id": "cur_standing"}},
		"Bearer bound":     {"admin": false, "cursor": map[string]any{"id": "cur_bound", "workspace": "ws_bound"}},
		"Bearer session-1": {"admin": false, "cursor": map[string]any{"id": "cur_session", "workspace": "ws_session"}},
	}}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		auth := r.Header.Get("Authorization")
		api.mu.Lock()
		api.hits = append(api.hits, sessionHit{r.Method, r.URL.Path, auth})
		api.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/me":
			json.NewEncoder(w).Encode(api.identity[auth])
		case r.Method == "POST" && r.URL.Path == "/v1/workspaces":
			if api.failWS {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "forbidden: needs workspace permission"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"id": "ws_session", "owner": "cur_standing"})
		case r.Method == "POST" && r.URL.Path == "/v1/cursors":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"cursor": map[string]any{"id": "cur_session", "workspace": "ws_session", "parent": "cur_standing"}, "token": "session-1"})
		default:
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	}))
	t.Cleanup(hs.Close)
	return New(&client.Client{URL: hs.URL, Token: token, HTTP: hs.Client()}), api
}

func callTool(t *testing.T, s *Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	res, rerr := s.handle(context.Background(), rpcReq{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})
	if rerr != nil {
		t.Fatalf("%s: rpc error %v", name, rerr)
	}
	m := res.(map[string]any)
	c := m["content"].([]content)
	isErr, _ := m["isError"].(bool)
	return c[0].Text, isErr
}

func TestSessionModeOffKeepsToken(t *testing.T) {
	s, api := newSessionServer(t, "standing", false)
	if txt, isErr := callTool(t, s, "workspace_info", map[string]any{"workspace": "w"}); isErr {
		t.Fatalf("workspace_info: %s", txt)
	}
	if txt, isErr := callTool(t, s, "cursor_report", map[string]any{"message": "hi"}); isErr {
		t.Fatalf("cursor_report: %s", txt)
	}
	if n := api.count("POST", "/v1/workspaces") + api.count("POST", "/v1/cursors"); n != 0 {
		t.Fatalf("without AGORA_MCP_SESSION nothing may be created, got %d creations: %v", n, api.log())
	}
	want := []sessionHit{
		{"GET", "/v1/me", "Bearer standing"},
		{"GET", "/v1/workspaces/w", "Bearer standing"},
		{"POST", "/v1/cursors/self/report", "Bearer standing"},
	}
	got := api.log()
	if len(got) != len(want) {
		t.Fatalf("requests: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSessionModeSwitchesToChildToken(t *testing.T) {
	t.Setenv("AGORA_MCP_SESSION", "1")
	s, api := newSessionServer(t, "standing", false)
	if txt, isErr := callTool(t, s, "workspace_info", nil); isErr {
		t.Fatalf("workspace_info: %s", txt)
	}
	if txt, isErr := callTool(t, s, "cursor_report", map[string]any{"message": "hi"}); isErr {
		t.Fatalf("cursor_report: %s", txt)
	}
	if txt, isErr := callTool(t, s, "browser_open", map[string]any{"url": "about:blank"}); isErr {
		t.Fatalf("browser_open: %s", txt)
	}
	if api.count("POST", "/v1/workspaces") != 1 || api.count("POST", "/v1/cursors") != 1 {
		t.Fatalf("want one workspace and one cursor created, got %v", api.log())
	}
	if s.c.Token != "session-1" || s.cursor != "cur_session" || s.ws != "ws_session" {
		t.Fatalf("session state: token switched=%v cursor=%q ws=%q", s.c.Token == "session-1", s.cursor, s.ws)
	}
	got := api.log()
	created := -1
	for i, h := range got {
		if h.method == "POST" && h.path == "/v1/cursors" {
			created = i
		}
		if created < 0 && h.auth != "Bearer standing" {
			t.Fatalf("request %d before the session cursor existed carried %q", i, h.auth)
		}
	}
	if created < 0 || created == len(got)-1 {
		t.Fatalf("no tool request after the session cursor was created: %v", got)
	}
	for _, h := range got[created+1:] {
		if h.auth != "Bearer session-1" {
			t.Fatalf("tool request %s %s carried %q, want the session cursor's token", h.method, h.path, h.auth)
		}
	}
	if got[created+1].path != "/v1/workspaces/ws_session" {
		t.Fatalf("workspace_info should default to the session workspace, got %s", got[created+1].path)
	}
}

func TestSessionModeFailureKeepsTokenAndReports(t *testing.T) {
	t.Setenv("AGORA_MCP_SESSION", "1")
	s, api := newSessionServer(t, "standing", true)
	txt, isErr := callTool(t, s, "workspace_info", map[string]any{"workspace": "w"})
	if !isErr || !strings.Contains(txt, "session mode") || !strings.Contains(txt, "create workspace") {
		t.Fatalf("first tool call should report the session failure, got isError=%v %q", isErr, txt)
	}
	txt, isErr = callTool(t, s, "cursor_report", map[string]any{"message": "hi"})
	if !isErr || !strings.Contains(txt, "session mode") {
		t.Fatalf("later tool calls should keep failing, got isError=%v %q", isErr, txt)
	}
	if s.c.Token != "standing" {
		t.Fatal("client token changed after a failed session setup")
	}
	if api.count("POST", "/v1/cursors") != 0 {
		t.Fatalf("no cursor may be created after the workspace failed: %v", api.log())
	}
	for _, h := range api.log() {
		if h.auth != "Bearer standing" {
			t.Fatalf("%s %s carried %q", h.method, h.path, h.auth)
		}
		if h.path != "/v1/me" && !(h.method == "POST" && h.path == "/v1/workspaces") {
			t.Fatalf("a tool request went out after the session failed: %s %s", h.method, h.path)
		}
	}
}

func TestSessionModeLeavesOtherTokensAlone(t *testing.T) {
	t.Setenv("AGORA_MCP_SESSION", "1")
	for _, tok := range []string{"admin", "bound"} {
		t.Run(tok, func(t *testing.T) {
			s, api := newSessionServer(t, tok, false)
			if txt, isErr := callTool(t, s, "workspace_info", map[string]any{"workspace": "w"}); isErr {
				t.Fatalf("workspace_info: %s", txt)
			}
			if n := api.count("POST", "/v1/workspaces") + api.count("POST", "/v1/cursors"); n != 0 {
				t.Fatalf("session mode must only act on a cursor without a workspace, got %v", api.log())
			}
			for _, h := range api.log() {
				if h.auth != "Bearer "+tok {
					t.Fatalf("%s %s carried %q", h.method, h.path, h.auth)
				}
			}
		})
	}
}
