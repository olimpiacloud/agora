package api

import (
	"strings"
	"testing"

	"github.com/olimpiacloud/agora/internal/core"
)

func TestLaunchedAgentLimitOverHTTP(t *testing.T) {
	t.Setenv("AGORA_CGROUPS", "off")
	e := newEnv(t, func(c *core.Config) { c.MaxAgents = 1 })
	ws := e.workspace()
	body := map[string]any{"workspace": ws, "launch": map[string]any{"adapter": "command", "command": []string{"sleep", "30"}}}
	if code, out := e.req(e.admin, "POST", "/v1/cursors", body); code != 201 {
		t.Fatalf("first launch: %d %v", code, out)
	}
	code, out := e.req(e.admin, "POST", "/v1/cursors", body)
	if code != 400 {
		t.Fatalf("second launch: %d %v", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "launched agent limit reached") {
		t.Fatalf("second launch error: %v", out)
	}
}
