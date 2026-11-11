package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/core"
)

func TestIdleReaper(t *testing.T) {
	h := newHWith(t, func(c *core.Config) { c.TabIdle = 4 * time.Second })
	ws := h.workspace("reaper")
	_, idle := h.cursor(h.admin, map[string]any{"name": "idle-agent", "workspace": ws})
	waitingID, waiting := h.cursor(h.admin, map[string]any{"name": "waiting-agent", "workspace": ws})
	idleTab := h.tab(idle, ws, h.site.URL+"/")
	waitingTab := h.tab(waiting, ws, h.site.URL+"/")
	h.do(waiting, "POST", "/v1/cursors/"+waitingID+"/status", map[string]any{"status": "needs_human", "detail": "captcha"})

	closed := h.waitEvent(ws, "scheduler.tab_closed", 30*time.Second)
	if d, _ := closed["data"].(map[string]any); d["target"] != idleTab {
		t.Fatalf("closed %v, want the idle tab %s", closed, idleTab)
	}
	tabs := h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/tabs")
	if len(tabs) != 1 || tabs[0]["target"] != waitingTab {
		t.Fatalf("tab of a cursor waiting for a human was closed: %v", tabs)
	}

	h.do(waiting, "POST", "/v1/cursors/"+waitingID+"/status", map[string]any{"status": "running"})
	h.waitEvent(ws, "scheduler.browser_stopped", 30*time.Second)
	if v := h.wsView(ws); v["browser"] == true {
		t.Fatalf("browser still running without tabs: %v", v)
	}
	if n := h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/tabs"); len(n) != 0 {
		t.Fatalf("tabs left: %v", n)
	}
	h.tab(idle, ws, h.site.URL+"/")

	h.do(waiting, "POST", "/v1/workspaces/"+ws+"/apps", map[string]any{"command": "sleep", "args": []string{"600"}, "restore": true})
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/suspend", nil)
	h.do(waiting, "POST", "/v1/cursors/"+waitingID+"/complete", map[string]any{"result": "done"})
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/resume", nil)
	stopped := h.waitEvent(ws, "scheduler.app_stopped", 30*time.Second)
	if stopped["cursor"] != waitingID {
		t.Fatalf("stopped %v, want the app of %s", stopped, waitingID)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		procs := h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/processes")
		if len(procs) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan app still running: %s", fmt.Sprint(procs))
		}
		time.Sleep(200 * time.Millisecond)
	}
}
