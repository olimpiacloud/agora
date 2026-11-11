package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/browser"
	"github.com/olimpiacloud/agora/internal/stream"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("AGORA_CGROUPS", "off")
	m, err := NewManager(Config{Home: t.TempDir(), RTC: stream.RTCOptions{IncludeLoop: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	return m
}

func newTestRuntime(t *testing.T, m *Manager) (string, *Runtime) {
	t.Helper()
	w, err := m.CreateWorkspace(context.Background(), CreateWorkspaceReq{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.ensureRuntime(context.Background(), w.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return w.ID, r
}

func setActivity(r *Runtime, at time.Time) {
	r.mu.Lock()
	r.activity = at
	r.mu.Unlock()
}

func wsState(t *testing.T, m *Manager, id string) WSState {
	t.Helper()
	w, err := m.GetWorkspace(id)
	if err != nil {
		t.Fatal(err)
	}
	return w.State
}

func TestSuspendKeepsDestroyedWorkspaceDestroyed(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	id, _ := newTestRuntime(t, m)
	if err := m.Destroy(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Suspend(ctx, id, "api"); !errors.Is(err, ErrState) {
		t.Fatalf("suspend of destroyed workspace: %v", err)
	}
	if st := wsState(t, m, id); st != StateDestroyed {
		t.Fatalf("state %s", st)
	}
	if _, err := m.ensureRuntime(ctx, id, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ensureRuntime of destroyed workspace: %v", err)
	}
	if err := m.Resume(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resume of destroyed workspace: %v", err)
	}
}

func TestSetStateCannotLeaveDestroyed(t *testing.T) {
	m := newTestManager(t)
	w, err := m.CreateWorkspace(context.Background(), CreateWorkspaceReq{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	m.setState(w.ID, StateDestroyed, "test")
	for _, st := range []WSState{StateActive, StateIdle, StateFrozen, StateSuspended, StateCreated} {
		m.setState(w.ID, st, "test")
		if got := wsState(t, m, w.ID); got != StateDestroyed {
			t.Fatalf("setState(%s) moved destroyed workspace to %s", st, got)
		}
	}
}

func TestSuspendIfIdleSkipsActiveRuntime(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	id, r := newTestRuntime(t, m)
	setActivity(r, time.Now())
	if !r.claimSuspend() {
		t.Fatal("claim failed")
	}
	if r.claimSuspend() {
		t.Fatal("second claim must fail while a suspend is pending")
	}
	if err := m.suspendIfIdle(ctx, id, r, time.Hour, "idle"); err != nil {
		t.Fatal(err)
	}
	if m.runtime(id) != r || wsState(t, m, id) != StateActive {
		t.Fatal("recently active runtime was suspended")
	}
	if !r.claimSuspend() {
		t.Fatal("flag not cleared after suspendIfIdle")
	}
	m.suspendIfIdle(ctx, id, r, time.Hour, "idle")
}

func TestSuspendIfIdleSkipsReplacedRuntime(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	id, r1 := newTestRuntime(t, m)
	setActivity(r1, time.Now().Add(-time.Hour))
	if err := m.suspendIfIdle(ctx, id, r1, time.Minute, "idle"); err != nil {
		t.Fatal(err)
	}
	if m.runtime(id) != nil || wsState(t, m, id) != StateSuspended {
		t.Fatal("idle runtime was not suspended")
	}
	r2, err := m.ensureRuntime(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if r2 == r1 {
		t.Fatal("resume reused the old runtime")
	}
	setActivity(r2, time.Now().Add(-time.Hour))
	if err := m.suspendIfIdle(ctx, id, r1, 0, "idle"); err != nil {
		t.Fatal(err)
	}
	if m.runtime(id) != r2 || wsState(t, m, id) != StateActive {
		t.Fatal("stale suspend tore down the resumed runtime")
	}
}

func TestMarkIdleRespectsRecentActivity(t *testing.T) {
	m := newTestManager(t)
	id, r := newTestRuntime(t, m)
	setActivity(r, time.Now())
	if m.markIdle(id, r, time.Minute) {
		t.Fatal("marked idle despite recent activity")
	}
	setActivity(r, time.Now().Add(-time.Hour))
	if !m.markIdle(id, r, time.Minute) || wsState(t, m, id) != StateIdle {
		t.Fatal("not marked idle")
	}
	r.touch()
	if wsState(t, m, id) != StateActive {
		t.Fatal("touch did not reactivate")
	}
}

func TestClosingRuntimeRefusesLaunches(t *testing.T) {
	m := newTestManager(t)
	_, r := newTestRuntime(t, m)
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	if _, err := r.launch(AppSpec{Command: "true"}); !errors.Is(err, ErrState) {
		t.Fatalf("launch on closing runtime: %v", err)
	}
	if _, err := r.Browser(context.Background()); !errors.Is(err, ErrState) {
		t.Fatalf("browser on closing runtime: %v", err)
	}
	r.mu.Lock()
	n := len(r.procs)
	r.closing = false
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d processes registered on a closing runtime", n)
	}
}

func TestCheckpointOfFrozenRuntimeUsesLastTabs(t *testing.T) {
	m := newTestManager(t)
	id, r := newTestRuntime(t, m)
	r.mu.Lock()
	r.frozen = true
	r.lastTabs = []TabRecord{{URL: "https://example.com"}}
	r.mu.Unlock()
	done := make(chan struct{})
	var cp *Checkpoint
	var err error
	go func() {
		cp, err = m.Checkpoint(context.Background(), id, "test", true)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint of frozen runtime hung")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.Tabs) != 1 || cp.Tabs[0].URL != "https://example.com" {
		t.Fatalf("tabs %+v", cp.Tabs)
	}
	r.mu.Lock()
	frozen := r.frozen
	r.mu.Unlock()
	if !frozen {
		t.Fatal("checkpoint thawed the runtime")
	}
}

func TestAllocPortsConcurrentDistinct(t *testing.T) {
	m := newTestManager(t)
	const n = 8
	bases := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bases[i], errs[i] = m.allocPorts(10)
		}(i)
	}
	wg.Wait()
	seen := map[int]bool{}
	for i, b := range bases {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if b == 0 || seen[b] {
			t.Fatalf("duplicate or zero port base %d in %v", b, bases)
		}
		seen[b] = true
	}
	for _, b := range bases {
		m.releasePorts(b)
	}
	m.mu.Lock()
	m.worktrees["wt_x"] = &Worktree{ID: "wt_x", PortBase: 21000}
	m.mu.Unlock()
	if m.holdPorts(21000) {
		t.Fatal("held a port block already used by a worktree")
	}
}

func TestProfileReservation(t *testing.T) {
	m := newTestManager(t)
	a, other := m.reserveProfile("p", "ws_a")
	if a == nil || other != "" {
		t.Fatalf("first reservation: %v %q", a, other)
	}
	if l, other := m.reserveProfile("p", "ws_b"); l != nil || other != "ws_a" {
		t.Fatalf("second workspace got the profile: %v %q", l, other)
	}
	a2, other := m.reserveProfile("p", "ws_a")
	if a2 == nil || other != "" {
		t.Fatal("same workspace could not reserve again")
	}
	m.releaseProfile("p", a)
	if l, other := m.reserveProfile("p", "ws_b"); l != nil || other != "ws_a" {
		t.Fatal("stale lease release freed a newer reservation")
	}
	m.releaseProfile("p", a2)
	b, other := m.reserveProfile("p", "ws_b")
	if b == nil || other != "" {
		t.Fatal("profile not free after release")
	}
	m.releaseProfile("p", b)
}

func TestProfileReservationConcurrent(t *testing.T) {
	m := newTestManager(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ws := []string{"ws_a", "ws_b", "ws_c", "ws_d"}[i%4]
			for j := 0; j < 200; j++ {
				l, other := m.reserveProfile("shared", ws)
				if other != "" {
					continue
				}
				m.releaseProfile("shared", l)
			}
		}(i)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("profile reservation deadlocked")
	}
	m.mu.Lock()
	left := len(m.profiles)
	m.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d leases leaked", left)
	}
}

func TestShutdownWaitsForLaunchExitWrites(t *testing.T) {
	m := newTestManager(t)
	id := newCursor(t, m, "agent", "")
	if err := m.setCursorStatus(id, CursorRunning, "", false); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if !m.registerLaunch(id, &launchProc{cmd: cmd}) {
		t.Fatal("launch not tracked before shutdown")
	}
	go func() {
		defer m.agents.Done()
		cmd.Wait()
		m.mu.Lock()
		delete(m.launches, id)
		m.mu.Unlock()
		time.Sleep(400 * time.Millisecond)
		m.Fail(id, "agent exited")
	}()
	m.Shutdown()
	b, err := os.ReadFile(m.store.path("cursors", id))
	if err != nil {
		t.Fatal(err)
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Status != CursorFailed {
		t.Fatalf("persisted status %s after shutdown", c.Status)
	}
}

func procState(t *testing.T, pid int) byte {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		t.Fatalf("bad stat %q", b)
	}
	return b[i+2]
}

func TestFrozenRuntimeWithBrowserSkipsCDP(t *testing.T) {
	m := newTestManager(t)
	id, r := newTestRuntime(t, m)
	orig := liveTabRecords
	var calls atomic.Int32
	liveTabRecords = func(*Runtime, *browser.Browser) []TabRecord {
		calls.Add(1)
		return nil
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		liveTabRecords = orig
		r.brMu.Lock()
		r.browser = nil
		r.brMu.Unlock()
		r.mu.Lock()
		delete(r.procs, pid)
		r.mu.Unlock()
		cmd.Process.Kill()
		cmd.Wait()
	})
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	for i := 0; procState(t, pid) != 'T'; i++ {
		if i > 200 {
			t.Fatal("process did not stop")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	r.procs[pid] = &Proc{Pid: pid, cmd: cmd, done: make(chan struct{})}
	r.frozen = true
	r.mu.Unlock()
	r.brMu.Lock()
	r.browser = &browser.Browser{}
	r.brMu.Unlock()
	run := func(name string, f func() error) {
		done := make(chan error, 1)
		go func() { done <- f() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s of frozen runtime hung", name)
		}
	}
	run("checkpoint", func() error {
		_, err := m.Checkpoint(context.Background(), id, "test", true)
		return err
	})
	run("fork", func() error {
		_, err := m.Fork(context.Background(), id, "")
		return err
	})
	if n := calls.Load(); n != 0 {
		t.Fatalf("CDP tab refresh called %d times on a frozen runtime", n)
	}
	if st := procState(t, pid); st != 'T' {
		t.Fatalf("frozen process state %c, SIGCONT was sent", st)
	}
	r.mu.Lock()
	frozen := r.frozen
	r.mu.Unlock()
	if !frozen {
		t.Fatal("runtime thawed")
	}
	r.mu.Lock()
	r.frozen = false
	r.mu.Unlock()
	r.currentTabs()
	if calls.Load() != 1 {
		t.Fatal("live tab refresh not used for a running runtime")
	}
}

func TestEndedCursorAppsWaitForReaper(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	id, r := newTestRuntime(t, m)
	start := func(owner string) *exec.Cmd {
		cmd := exec.Command("sleep", "30")
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
		r.procs[pid] = &Proc{Pid: pid, App: AppSpec{Owner: owner}, Started: time.Now(), cmd: cmd, done: done}
		r.mu.Unlock()
		t.Cleanup(func() {
			r.mu.Lock()
			delete(r.procs, pid)
			r.mu.Unlock()
			cmd.Process.Kill()
			<-done
		})
		return cmd
	}
	exited := func(cmd *exec.Cmd, wait time.Duration) bool {
		deadline := time.Now().Add(wait)
		for {
			if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", cmd.Process.Pid)); err != nil || bytes.Contains(b, []byte(") Z ")) {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	cursor := func(name string) string {
		c, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: name, Workspace: id})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	done, failed, cancelled := cursor("done"), cursor("failed"), cursor("cancelled")
	doneApp, failedApp, cancelledApp := start(done), start(failed), start(cancelled)
	if err := m.Complete(done, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Fail(failed, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelCursor(cancelled, "stop", false); err != nil {
		t.Fatal(err)
	}
	if !exited(cancelledApp, 3*time.Second) {
		t.Fatal("app of a cancelled cursor is still running")
	}
	if exited(doneApp, 300*time.Millisecond) || exited(failedApp, 0) {
		t.Fatal("app of a completed or failed cursor was stopped before the idle reaper")
	}
	if err := m.DeleteCursor(done); err != nil {
		t.Fatal(err)
	}
	if !exited(doneApp, 3*time.Second) {
		t.Fatal("app of a deleted cursor is still running")
	}
	if exited(failedApp, 0) {
		t.Fatal("deleting one cursor stopped the app of another")
	}
}

func TestQueuedSuspendYieldsToANewCaller(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	id, r := newTestRuntime(t, m)
	setActivity(r, time.Now().Add(-time.Hour))
	if !r.claimSuspend() {
		t.Fatal("suspend not claimed")
	}
	got, err := m.ensureRuntime(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != r {
		t.Fatal("caller got a different runtime")
	}
	m.suspendIfIdle(ctx, id, r, time.Minute, "idle")
	if m.runtime(id) != r {
		t.Fatal("queued suspend tore down the caller's runtime")
	}
	if s := wsState(t, m, id); s != StateActive {
		t.Fatalf("state %s, want active", s)
	}
}

func TestRestoreTabsKeepsTheListWhenNothingWasRestored(t *testing.T) {
	m := newTestManager(t)
	_, r := newTestRuntime(t, m)
	b := &browser.Browser{}
	r.brMu.Lock()
	r.browser = b
	r.brMu.Unlock()
	t.Cleanup(func() {
		r.brMu.Lock()
		r.browser = nil
		r.brMu.Unlock()
	})
	r.mu.Lock()
	r.lastTabs = []TabRecord{{URL: "about:blank"}}
	r.mu.Unlock()
	r.restoreTabs(b, []TabRecord{{URL: "about:blank"}})
	r.mu.Lock()
	n := len(r.lastTabs)
	r.mu.Unlock()
	if n != 1 {
		t.Fatalf("restore list has %d entries after a restore that restored nothing", n)
	}
}

func TestBrowserCallRecordsACrashedBrowser(t *testing.T) {
	m := newTestManager(t)
	id, r := newTestRuntime(t, m)
	orig := browserExited
	browserExited = func(*browser.Browser) bool { return true }
	t.Cleanup(func() {
		browserExited = orig
		r.brMu.Lock()
		r.browser = nil
		r.brMu.Unlock()
	})
	r.brMu.Lock()
	r.browser = &browser.Browser{}
	r.brMu.Unlock()
	if _, err := r.Browser(context.Background()); err == nil {
		t.Fatal("relaunch without a display succeeded")
	}
	if n := len(m.Bus.Recent(Filter{Workspace: id, Types: []string{"browser.crashed"}}, 5)); n != 1 {
		t.Fatalf("%d browser.crashed events", n)
	}
}
