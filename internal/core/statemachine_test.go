package core

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

func attachSleep(t *testing.T, m *Manager, id string) (*exec.Cmd, chan struct{}) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if !m.registerLaunch(id, &launchProc{cmd: cmd}) {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("launch not tracked")
	}
	exited := make(chan struct{})
	go func() {
		defer m.agents.Done()
		cmd.Wait()
		m.mu.Lock()
		delete(m.launches, id)
		m.mu.Unlock()
		close(exited)
	}()
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	})
	return cmd, exited
}

func waitState(t *testing.T, pid int, want func(byte) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if want(procState(t, pid)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d stuck in state %c", pid, procState(t, pid))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func stopped(s byte) bool    { return s == 'T' }
func notStopped(s byte) bool { return s != 'T' }

func TestLeavingPausedResumesTheProcess(t *testing.T) {
	m := newTestManager(t)
	id := newCursor(t, m, "agent", "")
	cmd, _ := attachSleep(t, m, id)
	if err := m.Pause(id); err != nil {
		t.Fatal(err)
	}
	waitState(t, cmd.Process.Pid, stopped)
	if err := m.SetStatus(id, CursorNeedHuman, "challenge"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetStatus(id, CursorRunning, ""); err != nil {
		t.Fatal(err)
	}
	waitState(t, cmd.Process.Pid, notStopped)
}

func TestCancellingAPausedCursorEndsItsProcessPromptly(t *testing.T) {
	m := newTestManager(t)
	id := newCursor(t, m, "agent", "")
	cmd, exited := attachSleep(t, m, id)
	if err := m.Pause(id); err != nil {
		t.Fatal(err)
	}
	waitState(t, cmd.Process.Pid, stopped)
	if err := m.CancelCursor(id, "test", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("paused process still alive 2s after cancel")
	}
}

func TestCascadeCancelEndsTheParentFirst(t *testing.T) {
	m := newTestManager(t)
	parent := newCursor(t, m, "p", "")
	child := newCursor(t, m, "c", parent)
	if err := m.CancelCursor(parent, "test", true); err != nil {
		t.Fatal(err)
	}
	var ps, cs int64
	for _, e := range m.Bus.Recent(Filter{Types: []string{"cursor.ended"}}, 10) {
		switch e.Cursor {
		case parent:
			ps = e.Seq
		case child:
			cs = e.Seq
		}
	}
	if ps == 0 || cs == 0 {
		t.Fatalf("missing cursor.ended events: parent %d child %d", ps, cs)
	}
	if ps >= cs {
		t.Fatalf("parent ended at seq %d, after child at %d", ps, cs)
	}
	for _, id := range []string{parent, child} {
		c, err := m.GetCursor(id)
		if err != nil {
			t.Fatal(err)
		}
		if c.Status != CursorCancelled {
			t.Fatalf("%s is %s", id, c.Status)
		}
	}
}

func TestCascadeCancelLeavesNoLiveChild(t *testing.T) {
	m := newTestManager(t)
	parent := newCursor(t, m, "p", "")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			m.CreateCursor(context.Background(), CreateCursorReq{Parent: parent})
		}
	}()
	time.Sleep(10 * time.Millisecond)
	m.CancelCursor(parent, "test", true)
	close(stop)
	wg.Wait()
	if live := m.ListCursors(CursorQuery{Parent: parent, Active: true}); len(live) != 0 {
		t.Fatalf("%d children alive under a cancelled parent", len(live))
	}
}

func countEvents(m *Manager, ws, typ string) int {
	return len(m.Bus.Recent(Filter{Workspace: ws, Types: []string{typ}}, 100))
}

func TestDestroyTwiceIsQuiet(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	w, err := m.CreateWorkspace(ctx, CreateWorkspaceReq{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := m.Destroy(ctx, w.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	if n := countEvents(m, w.ID, "workspace.destroyed"); n != 1 {
		t.Fatalf("%d workspace.destroyed events", n)
	}
	if err := m.Destroy(ctx, w.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetWorkspace(w.ID); err != ErrNotFound {
		t.Fatalf("purge left the workspace: %v", err)
	}
}

func TestExpiredWorkspaceIsDestroyedOnce(t *testing.T) {
	m := newTestManager(t)
	w, err := m.CreateWorkspace(context.Background(), CreateWorkspaceReq{Name: "t", Config: WorkspaceConfig{TTL: Duration{Duration: time.Nanosecond}}})
	if err != nil {
		t.Fatal(err)
	}
	l := m.wsLock(w.ID)
	l.Lock()
	for i := 0; i < 3; i++ {
		m.lifecycleTick()
	}
	l.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for wsState(t, m, w.ID) != StateDestroyed {
		if time.Now().After(deadline) {
			t.Fatal("expired workspace not destroyed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	check := func() {
		t.Helper()
		if n := countEvents(m, w.ID, "workspace.expired"); n != 1 {
			t.Fatalf("%d workspace.expired events", n)
		}
		if n := countEvents(m, w.ID, "workspace.destroyed"); n != 1 {
			t.Fatalf("%d workspace.destroyed events", n)
		}
	}
	check()
	m.lifecycleTick()
	time.Sleep(50 * time.Millisecond)
	check()
}
