package api

import "testing"

func TestWebGLOptionIsAdminOnly(t *testing.T) {
	e := newEnv(t)
	code, out := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"webgl": "software"}})
	if code != 201 {
		t.Fatalf("admin webgl software: %d %v", code, out)
	}
	if got := out["config"].(map[string]any)["webgl"]; got != "software" {
		t.Fatalf("config.webgl = %v, want software", got)
	}
	if code, out := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"webgl": "fast"}}); code != 400 {
		t.Fatalf("admin webgl fast: %d %v, want 400", code, out)
	}
	ws := e.workspace()
	_, tok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"workspace"}})
	if code, out := e.req(tok, "POST", "/v1/workspaces", map[string]any{"config": map[string]any{"webgl": "software"}}); code != 403 {
		t.Fatalf("cursor webgl software: %d %v, want 403", code, out)
	}
	code, out = e.req(e.admin, "POST", "/v1/workspaces", map[string]any{})
	if code != 201 {
		t.Fatalf("plain workspace: %d %v", code, out)
	}
	if _, ok := out["config"].(map[string]any)["webgl"]; ok {
		t.Fatalf("plain workspace config has webgl: %v", out["config"])
	}
}
