package sandbox

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

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
