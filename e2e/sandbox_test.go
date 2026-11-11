package e2e

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/sandbox"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == sandbox.Cmd {
		sandbox.Main(os.Args[2:])
		return
	}
	os.Exit(m.Run())
}

func TestSandboxedWorkspace(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sandbox needs root")
	}
	if _, err := user.Lookup("agora"); err != nil {
		t.Skip("apps user agora missing")
	}
	h := newH(t)
	w := h.do(h.admin, "POST", "/v1/workspaces", map[string]any{"name": "sb", "start": true, "config": map[string]any{"sandbox": true}})
	ws := w["id"].(string)

	tg := h.tab(h.admin, ws, h.site.URL+"/")
	h.waitEval(h.admin, ws, tg, "document.readyState", "complete", 20*time.Second)

	home := filepath.Join(h.home, "workspaces", ws, "home")
	script := `id -u > r_id; ls -A /root > r_root 2>&1; ls -A ` + h.home + ` > r_state 2>&1; (echo x > /usr/agora-x) 2> r_usr; ls /proc | grep -c '^[0-9]' > r_procs; ls -A /dev | tr '\n' ' ' > r_dev; awk '$2=="/"{print $4}' /proc/mounts > r_rootopts; (exec 3<>/dev/ptmx && echo ok) > r_ptmx 2>&1; head -c 4 /dev/urandom | wc -c > r_urandom; touch r_done`
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
	u, _ := user.Lookup("agora")
	if got := read("r_id"); got != u.Uid {
		t.Errorf("app uid %q, want %s", got, u.Uid)
	}
	if got := read("r_root"); got != "" {
		t.Errorf("/root visible in sandbox: %q", got)
	}
	if got := read("r_state"); strings.Contains(got, "admin.token") || strings.Contains(got, "state") {
		t.Errorf("agora state visible in sandbox: %q", got)
	}
	if got := read("r_usr"); !strings.Contains(got, "Read-only") {
		t.Errorf("system not read-only: %q", got)
	}
	if got := read("r_procs"); got == "" || len(got) > 2 {
		t.Errorf("sandbox sees %s processes; expected only its own", got)
	}
	if got := strings.Fields(read("r_dev")); strings.Join(got, " ") != "fd full null ptmx pts random shm stderr stdin stdout tty urandom zero" {
		t.Errorf("sandbox /dev has %v", got)
	}
	if got := read("r_rootopts"); !strings.Contains(got, "ro") || !strings.Contains(got, "nosuid") || !strings.Contains(got, "nodev") {
		t.Errorf("sandbox root mount options %q", got)
	}
	if got := read("r_ptmx"); got != "ok" {
		t.Errorf("/dev/ptmx unusable in sandbox: %q", got)
	}
	if got := read("r_urandom"); got != "4" {
		t.Errorf("/dev/urandom unreadable in sandbox: %q", got)
	}

	renderers := 0
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		cmd, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || !strings.Contains(string(cmd), "--type=renderer") || !strings.Contains(string(cmd), ws) {
			continue
		}
		st, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "status"))
		renderers++
		if !strings.Contains(string(st), "Seccomp:\t2") {
			t.Errorf("renderer %s runs without seccomp: Chrome sandbox is off", e.Name())
		}
	}
	if renderers == 0 {
		t.Error("no renderer process found for the sandboxed workspace")
	}
}

func TestWorkspacesAreSandboxedByDefault(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sandbox needs root")
	}
	if _, err := user.Lookup("agora"); err != nil {
		t.Skip("apps user agora missing")
	}
	h := newH(t)
	ws := h.workspace("dflt")
	if cfg := h.do(h.admin, "GET", "/v1/workspaces/"+ws, nil)["config"].(map[string]any); cfg["sandbox"] != true {
		t.Fatalf("workspace created without \"sandbox\" is not sandboxed: %v", cfg)
	}
	tg := h.tab(h.admin, ws, h.site.URL+"/")
	h.waitEval(h.admin, ws, tg, "document.readyState", "complete", 20*time.Second)
	self, _ := os.Readlink("/proc/self/ns/pid")
	found := false
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		cmd, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || !strings.Contains(string(cmd), "--type=renderer") || !strings.Contains(string(cmd), ws) {
			continue
		}
		found = true
		if ns, _ := os.Readlink(filepath.Join("/proc", e.Name(), "ns", "pid")); ns == self {
			t.Errorf("renderer %s shares the daemon's pid namespace", e.Name())
		}
	}
	if !found {
		t.Error("no renderer process found for the workspace")
	}
	off := h.do(h.admin, "POST", "/v1/workspaces", map[string]any{"name": "nosb", "config": map[string]any{"sandbox": false}})
	if cfg := off["config"].(map[string]any); cfg["sandbox"] == true {
		t.Fatalf("admin could not opt out of the sandbox: %v", cfg)
	}
}
