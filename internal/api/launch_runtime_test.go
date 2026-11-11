package api

import (
	"strings"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/core"
)

func TestClampLaunchRuntime(t *testing.T) {
	cases := []struct {
		daemon, req, want time.Duration
	}{
		{0, 0, 0},
		{0, time.Hour, time.Hour},
		{10 * time.Minute, 0, 10 * time.Minute},
		{10 * time.Minute, 5 * time.Minute, 5 * time.Minute},
		{10 * time.Minute, time.Hour, 10 * time.Minute},
	}
	for _, c := range cases {
		l := &core.LaunchSpec{MaxRuntime: core.Duration{Duration: c.req}}
		clampLaunchRuntime(l, c.daemon)
		if l.MaxRuntime.Duration != c.want {
			t.Errorf("daemon %s, request %s: got %s, want %s", c.daemon, c.req, l.MaxRuntime.Duration, c.want)
		}
	}
	clampLaunchRuntime(nil, time.Minute)
}

func TestAdminLaunchMaxRuntimeOverHTTP(t *testing.T) {
	t.Setenv("AGORA_CGROUPS", "off")
	e := newEnv(t)
	ws := e.workspace()
	code, out := e.req(e.admin, "POST", "/v1/cursors", map[string]any{"workspace": ws, "launch": map[string]any{"adapter": "command", "command": []string{"sleep", "30"}, "max_runtime": "300ms"}})
	if code != 201 {
		t.Fatalf("launch: %d %v", code, out)
	}
	id := out["cursor"].(map[string]any)["id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, c := e.req(e.admin, "GET", "/v1/cursors/"+id, nil)
		if c["status"] == "failed" {
			if msg, _ := c["error"].(string); !strings.Contains(msg, "maximum runtime") {
				t.Fatalf("error: %v", c["error"])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cursor still %v after 3 seconds", c["status"])
		}
		time.Sleep(20 * time.Millisecond)
	}
}
