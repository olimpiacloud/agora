package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == Cmd {
		Main(os.Args[2:])
		return
	}
	os.Exit(m.Run())
}

func TestHelperDropsPrivileges(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "grep -E '^(Uid|CapBnd|NoNewPrivs):' /proc/self/status"
	argv := append(Wrap(self, Spec{Uid: 65534, Gid: 65534}), "sh", "-c", script)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = Attr()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v: %s", err, out)
	}
	lines := map[string]bool{}
	uid := ""
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		if f[0] == "Uid:" && len(f) > 1 {
			uid = f[1]
		}
		lines[strings.Join(f, " ")] = true
	}
	if uid != "65534" {
		t.Errorf("uid %q, want 65534: %s", uid, out)
	}
	if !lines["CapBnd: 0000000000000000"] {
		t.Errorf("capability bounding set not empty: %s", out)
	}
	if !lines["NoNewPrivs: 1"] {
		t.Errorf("no_new_privs not set: %s", out)
	}
}

func TestHelperJoinsNetworkNamespace(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	holder := exec.Command("sleep", "60")
	holder.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		holder.Process.Kill()
		holder.Wait()
	})
	path := fmt.Sprintf("/proc/%d/ns/net", holder.Process.Pid)
	theirs, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	ours, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if theirs == ours {
		t.Fatalf("holder shares the test's network namespace %s", ours)
	}
	script := "readlink /proc/self/ns/net; tail -n +3 /proc/net/dev | cut -d: -f1 | tr -d ' ' | sort | tr '\\n' ' '"
	probe := func(netns string) []string {
		argv := append(Wrap(self, Spec{Uid: 65534, Gid: 65534, NetNS: netns}), "sh", "-c", script)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.SysProcAttr = Attr()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("helper: %v: %s", err, out)
		}
		lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
		for len(lines) < 2 {
			lines = append(lines, "")
		}
		return []string{strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])}
	}
	got := probe(path)
	if got[0] != theirs {
		t.Errorf("helper runs in %q, want the holder's %q", got[0], theirs)
	}
	if got[1] != "lo" {
		t.Errorf("interfaces %q, want only lo", got[1])
	}
	if got := probe(""); got[0] != ours {
		t.Errorf("without -netns the helper runs in %q, want the caller's %q", got[0], ours)
	}
}

func TestRootIsReadOnlyNosuidNodev(t *testing.T) {
	want := uint64(unix.MOUNT_ATTR_RDONLY | unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NODEV)
	if rootAttr&want != want {
		t.Fatalf("root attributes %#x lack %#x", rootAttr, want)
	}
}

func TestDevMountPlan(t *testing.T) {
	ms := devMounts("/r/dev")
	if len(ms) == 0 || ms[0].dst != "/r/dev" || ms[0].fstype != "tmpfs" || !strings.Contains(ms[0].data, "mode=0755") {
		t.Fatalf("/dev must be a fresh 0755 tmpfs first: %+v", ms)
	}
	if ms[0].flags&(unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC) != unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC {
		t.Fatalf("/dev tmpfs flags %#x", ms[0].flags)
	}
	binds := map[string]bool{}
	var pts, shm *mnt
	for i := range ms[1:] {
		m := &ms[i+1]
		if !strings.HasPrefix(m.dst, "/r/dev/") {
			t.Fatalf("mount outside the new /dev: %+v", m)
		}
		switch m.fstype {
		case "":
			if m.flags != unix.MS_BIND || m.src != "/dev/"+strings.TrimPrefix(m.dst, "/r/dev/") {
				t.Fatalf("device bind %+v", m)
			}
			binds[strings.TrimPrefix(m.src, "/dev/")] = true
		case "devpts":
			pts = m
		case "tmpfs":
			shm = m
		default:
			t.Fatalf("unexpected mount %+v", m)
		}
	}
	want := []string{"null", "zero", "full", "random", "urandom", "tty"}
	if len(binds) != len(want) {
		t.Fatalf("device binds %v, want %v", binds, want)
	}
	for _, n := range want {
		if !binds[n] {
			t.Fatalf("missing /dev/%s", n)
		}
	}
	if pts == nil || pts.dst != "/r/dev/pts" || !strings.Contains(pts.data, "newinstance") || !strings.Contains(pts.data, "ptmxmode=0666") {
		t.Fatalf("devpts %+v", pts)
	}
	if shm == nil || shm.dst != "/r/dev/shm" || !strings.Contains(shm.data, "mode=1777") || shm.flags&unix.MS_NODEV == 0 {
		t.Fatalf("/dev/shm %+v", shm)
	}
	links := map[string]string{}
	for _, l := range devLinks {
		links[l[0]] = l[1]
	}
	for n, target := range map[string]string{"ptmx": "pts/ptmx", "fd": "/proc/self/fd", "stdin": "/proc/self/fd/0", "stdout": "/proc/self/fd/1", "stderr": "/proc/self/fd/2"} {
		if links[n] != target {
			t.Fatalf("/dev/%s -> %q, want %q", n, links[n], target)
		}
	}
}

func TestCatchesTerm(t *testing.T) {
	if !catchesTerm(os.Getpid()) {
		t.Fatal("the Go runtime handles SIGTERM in this process")
	}
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()
	if catchesTerm(cmd.Process.Pid) {
		t.Fatal("sleep does not handle SIGTERM")
	}
	start := time.Now()
	WaitSignals(cmd.Process.Pid, 50*time.Millisecond)
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("WaitSignals returned before the timeout for a process without a handler")
	}
}
