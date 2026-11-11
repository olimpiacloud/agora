package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/sandbox"
)

func TestExperimentalNetns(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sandbox needs root")
	}
	if _, err := user.Lookup("agora"); err != nil {
		t.Skip("apps user agora missing")
	}
	t.Setenv("AGORA_EXPERIMENTAL_NETNS", "1")
	h := newH(t)
	w := h.do(h.admin, "POST", "/v1/workspaces", map[string]any{"name": "net", "start": true, "config": map[string]any{"sandbox": true}})
	ws := w["id"].(string)

	tg := h.tab(h.admin, ws, "data:text/html,<title>ok</title><p>hello")
	h.waitEval(h.admin, ws, tg, "document.title", "ok", 20*time.Second)
	var wins []map[string]any
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && len(wins) == 0; time.Sleep(100 * time.Millisecond) {
		wins = h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/windows")
	}
	if len(wins) == 0 {
		t.Fatal("Chrome has no window on the workspace's X server")
	}

	home := filepath.Join(h.home, "workspaces", ws, "home")
	script := `tail -n +3 /proc/net/dev | cut -d: -f1 | tr -d ' ' | sort | tr '\n' ' ' > r_ifs; readlink /proc/self/ns/net > r_ns; touch r_done`
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/apps", map[string]any{"command": "sh", "args": []string{"-c", script}})
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "r_done")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sandboxed app did not finish")
		}
		time.Sleep(100 * time.Millisecond)
	}
	read := func(n string) string {
		b, _ := os.ReadFile(filepath.Join(home, n))
		return strings.TrimSpace(string(b))
	}
	if got := read("r_ifs"); got != "lo" {
		t.Errorf("sandboxed app sees interfaces %q, want only lo", got)
	}
	appNS := read("r_ns")
	ours, _ := os.Readlink("/proc/self/ns/net")

	browserNS := ""
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		cmd, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		c := string(cmd)
		if err != nil || !strings.Contains(c, "--remote-debugging-pipe") || !strings.Contains(c, ws) || strings.Contains(c, "--type=") || strings.Contains(c, sandbox.Cmd) {
			continue
		}
		browserNS, _ = os.Readlink(filepath.Join("/proc", e.Name(), "ns", "net"))
		break
	}
	if browserNS == "" {
		t.Fatal("no browser process found for the workspace")
	}
	if appNS == "" || appNS != browserNS {
		t.Errorf("app runs in %q, Chrome in %q: want one namespace per workspace", appNS, browserNS)
	}
	if browserNS == ours {
		t.Fatalf("Chrome runs in the host network namespace %s", ours)
	}

	if _, err := exec.LookPath("python3"); err == nil {
		h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/apps", map[string]any{"command": "python3", "args": []string{"-m", "http.server", "8765", "--bind", "127.0.0.1"}})
		inner := h.tab(h.admin, ws, "about:blank")
		served := false
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end) && !served; time.Sleep(300 * time.Millisecond) {
			var out map[string]any
			h.admin.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+inner, map[string]any{"action": "navigate", "url": "http://127.0.0.1:8765/"}, &out)
			served = strings.Contains(fmt.Sprint(h.eval(h.admin, ws, inner, "document.title")), "Directory listing")
		}
		if !served {
			t.Error("Chrome cannot reach a server an app of the same workspace listens on at 127.0.0.1")
		}
	}

	var out map[string]any
	h.admin.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tg, map[string]any{"action": "navigate", "url": h.site.URL + "/work?n=net", "timeout_ms": 5000}, &out)
	if got := fmt.Sprint(h.eval(h.admin, ws, tg, "document.title")); got == "Work net" {
		t.Errorf("the host's loopback site loaded inside the isolated workspace (title %q)", got)
	}

	holders := func() int {
		n := 0
		ents, _ := os.ReadDir("/proc")
		for _, e := range ents {
			if l, err := os.Readlink(filepath.Join("/proc", e.Name(), "ns", "net")); err == nil && l == browserNS {
				n++
			}
		}
		return n
	}
	if holders() == 0 {
		t.Fatal("no process holds the workspace namespace before suspend")
	}
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/suspend", nil)
	if n := holders(); n != 0 {
		t.Errorf("%d processes still in the workspace namespace after suspend", n)
	}
}
