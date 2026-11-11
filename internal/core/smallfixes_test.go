package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/stream"
)

func TestTabBudgetHoldsUnderConcurrentOpens(t *testing.T) {
	r := &Runtime{}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.beginOpen(3, 5) {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := ok.Load(); n != 2 {
		t.Fatalf("%d opens passed the budget, want 2", n)
	}
	r.endOpen()
	r.endOpen()
	if !r.beginOpen(3, 5) {
		t.Fatal("budget not released after the opens finished")
	}
	if !(&Runtime{}).beginOpen(100, 0) {
		t.Fatal("limit 0 refused an open")
	}
}

func TestAppReaperSignalsOnceThenKills(t *testing.T) {
	t.Setenv("AGORA_CGROUPS", "off")
	m, err := NewManager(Config{Home: t.TempDir(), RTC: stream.RTCOptions{IncludeLoop: true}, TabIdle: time.Millisecond, AppKillGrace: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	ctx := context.Background()
	id, r := newTestRuntime(t, m)
	r.mu.Lock()
	r.reaping = true
	r.mu.Unlock()
	t.Cleanup(func() {
		r.mu.Lock()
		r.reaping = false
		r.mu.Unlock()
	})
	c, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "o", Workspace: id})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Complete(c.ID, nil); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `trap "" TERM; sleep 30`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	pid := cmd.Process.Pid
	r.mu.Lock()
	r.procs[pid] = &Proc{Pid: pid, App: AppSpec{Owner: c.ID}, Started: time.Now(), cmd: cmd, done: done}
	r.mu.Unlock()
	t.Cleanup(func() {
		r.mu.Lock()
		delete(r.procs, pid)
		r.mu.Unlock()
		syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	})
	time.Sleep(200 * time.Millisecond)
	alive := func() bool {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		return err == nil && !bytes.Contains(b, []byte(") Z "))
	}
	stopped := func() int {
		return len(m.Bus.Recent(Filter{Workspace: id, Types: []string{"scheduler.app_stopped"}}, 10))
	}
	m.reapApps(id, r)
	m.reapApps(id, r)
	if n := stopped(); n != 1 {
		t.Fatalf("%d scheduler.app_stopped events after two passes", n)
	}
	time.Sleep(20 * time.Millisecond)
	if !alive() {
		t.Fatal("app ignoring SIGTERM died after the first pass")
	}
	time.Sleep(150 * time.Millisecond)
	m.reapApps(id, r)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("app ignoring SIGTERM not killed after the grace period")
	}
	if n := stopped(); n != 1 {
		t.Fatalf("%d scheduler.app_stopped events after the kill", n)
	}
}

func TestTabClosedDropsItsObservation(t *testing.T) {
	m := newTestManager(t)
	id, r := newTestRuntime(t, m)
	r.mu.Lock()
	r.lastObs = map[string]string{"T1": "x", "T2": "y"}
	r.mu.Unlock()
	r.onBrowserEvent("browser.tab.closed", map[string]any{"target": "T1"})
	r.mu.Lock()
	_, t1 := r.lastObs["T1"]
	_, t2 := r.lastObs["T2"]
	r.mu.Unlock()
	if t1 || !t2 {
		t.Fatalf("lastObs after closing T1: T1=%v T2=%v", t1, t2)
	}
	if n := len(m.Bus.Recent(Filter{Workspace: id, Types: []string{"browser.tab.closed"}}, 1)); n != 1 {
		t.Fatalf("%d browser.tab.closed events", n)
	}
}

func TestTransferMovesTheSubtree(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	create := func(name, parent string) *Cursor {
		t.Helper()
		c, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: name, Parent: parent})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a := create("a", "")
	b := create("b", a.ID)
	c := create("c", b.ID)
	d := create("d", "")
	check := func(id, root string, depth int) {
		t.Helper()
		got, err := m.GetCursor(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Root != root || got.Depth != depth {
			t.Fatalf("%s: root %s depth %d, want %s %d", id, got.Root, got.Depth, root, depth)
		}
	}
	pid := d.ID
	if _, err := m.Transfer(b.ID, TransferReq{Parent: &pid}); err != nil {
		t.Fatal(err)
	}
	check(c.ID, d.Root, 2)
	empty := ""
	if _, err := m.Transfer(b.ID, TransferReq{Parent: &empty}); err != nil {
		t.Fatal(err)
	}
	check(b.ID, b.ID, 0)
	check(c.ID, b.ID, 1)
}

func TestResizeAllowed(t *testing.T) {
	if err := resizeAllowed(1920, 1200, 1920, 1200); err != nil {
		t.Fatal(err)
	}
	if err := resizeAllowed(2560, 1440, 1920, 1200); err == nil || !strings.Contains(err.Error(), "1920x1200") {
		t.Fatalf("oversize: %v", err)
	}
	if err := resizeAllowed(2560, 1440, 2560, 1440); err != nil {
		t.Fatal(err)
	}
	if err := resizeAllowed(100, 100, 1920, 1200); err == nil {
		t.Fatal("undersize allowed")
	}
}

func TestUsageTickReplacesTheRecord(t *testing.T) {
	m := newTestManager(t)
	id, _ := newTestRuntime(t, m)
	before := &WorkspaceUsage{Time: time.Now().UTC(), Detailed: true, Procs: 77, ByRole: map[string]float64{}}
	m.mu.Lock()
	m.usage[id] = before
	m.mu.Unlock()
	m.usageTick()
	m.mu.Lock()
	after := m.usage[id]
	m.mu.Unlock()
	if after == before {
		t.Fatal("usage tick modified the served record in place")
	}
	if after == nil || !after.Detailed {
		t.Fatalf("detailed record lost: %+v", after)
	}
	if before.Procs != 77 {
		t.Fatalf("old record modified: procs %d", before.Procs)
	}
}
