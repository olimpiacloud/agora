package core

import (
	"strings"
	"testing"
	"time"
)

func TestControlViewerNoopTakesNoLock(t *testing.T) {
	m := &Manager{Arb: NewArbiter(nil), ws: map[string]*Workspace{}}
	r := &Runtime{m: m, id: "ws1", viewers: map[string]*Viewer{}}
	v := &Viewer{ID: "v1", Workspace: "ws1", Control: true, r: r, m: m, held: map[uint32]bool{}, buttons: map[int]bool{}}
	r.viewers[v.ID] = v
	v.Input([]byte(`{"t":"noop"}`))
	if l := m.Arb.List("ws1"); len(l) != 0 {
		t.Fatalf("noop from a control viewer created locks: %+v", l)
	}
	if !r.human.IsZero() {
		t.Fatal("noop from a control viewer counted as human input")
	}
	v.Input([]byte(`{"t":"move","x":1,"y":1}`))
	if l := m.Arb.List("ws1"); len(l) != 1 || l[0].Holder != "human:v1" {
		t.Fatalf("move from a control viewer should take the workspace lock, got %+v", l)
	}
}

func TestDetachedViewerTakeLeavesNoLock(t *testing.T) {
	m := &Manager{Arb: NewArbiter(nil), Bus: NewBus(t.TempDir(), 16), ws: map[string]*Workspace{}}
	r := &Runtime{m: m, id: "ws1", viewers: map[string]*Viewer{}}
	v := &Viewer{ID: "v1", Workspace: "ws1", Control: true, r: r, m: m, held: map[uint32]bool{}, buttons: map[int]bool{}}
	r.viewers[v.ID] = v
	v.Input([]byte(`{"t":"take"}`))
	if l := m.Arb.List("ws1"); len(l) != 1 || l[0].Holder != "human:v1" {
		t.Fatalf("take from an attached viewer should hold the workspace, got %+v", l)
	}
	v.Input([]byte(`{"t":"release"}`))
	if l := m.Arb.List("ws1"); len(l) != 0 {
		t.Fatalf("release left locks: %+v", l)
	}
	delete(r.viewers, v.ID)
	v.Input([]byte(`{"t":"take"}`))
	if l := m.Arb.List("ws1"); len(l) != 0 {
		t.Fatalf("take after detach left a lock: %+v", l)
	}
	r.viewers[v.ID] = v
	v.mu.Lock()
	v.closed = true
	v.mu.Unlock()
	v.Input([]byte(`{"t":"take"}`))
	if l := m.Arb.List("ws1"); len(l) != 0 {
		t.Fatalf("take from a closed viewer left a lock: %+v", l)
	}
}

func TestViewerHelloRedactsCursors(t *testing.T) {
	m := &Manager{Arb: NewArbiter(nil), ws: map[string]*Workspace{}, cursors: map[string]*Cursor{}}
	m.cursors["cur_1"] = &Cursor{ID: "cur_1", Name: "a", Workspace: "ws1", Status: CursorRunning, TokenHash: "deadbeef", Launch: &LaunchSpec{Adapter: "command", Env: map[string]string{"API_KEY": "hunter2"}}}
	r := &Runtime{m: m, id: "ws1"}
	var got []byte
	v := &Viewer{ID: "v1", Workspace: "ws1", r: r, m: m, held: map[uint32]bool{}, buttons: map[int]bool{}, send: func(b []byte) error {
		got = append([]byte(nil), b...)
		return nil
	}}
	v.sendHello()
	s := string(got)
	if s == "" {
		t.Fatal("no hello sent")
	}
	if !strings.Contains(s, `"cur_1"`) {
		t.Fatalf("hello does not list the cursor: %s", s)
	}
	for _, leak := range []string{"token_hash", "deadbeef", "hunter2", "API_KEY"} {
		if strings.Contains(s, leak) {
			t.Fatalf("hello leaks %q: %s", leak, s)
		}
	}
}

func idleTestRuntime(t *testing.T) (*Manager, string, *Runtime) {
	t.Helper()
	m := newTestManager(t)
	id, r := newTestRuntime(t, m)
	setActivity(r, time.Now().Add(-time.Hour))
	if !m.markIdle(id, r, time.Minute) {
		t.Fatal("runtime not marked idle")
	}
	if s := wsState(t, m, id); s != StateIdle {
		t.Fatalf("state %s, want idle", s)
	}
	return m, id, r
}

func TestDetachedViewerInputDoesNotTouchRuntime(t *testing.T) {
	m, id, r := idleTestRuntime(t)
	v := &Viewer{ID: "v1", Workspace: id, Control: true, r: r, m: m, held: map[uint32]bool{}, buttons: map[int]bool{}}
	v.Input([]byte(`{"t":"move","x":1,"y":1}`))
	if s := wsState(t, m, id); s != StateIdle {
		t.Fatalf("detached viewer input made the workspace %s", s)
	}
	if l := m.Arb.List(id); len(l) != 0 {
		t.Fatalf("detached viewer input took locks: %+v", l)
	}
}

func TestTouchIgnoresClosingRuntime(t *testing.T) {
	m, id, r := idleTestRuntime(t)
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	t.Cleanup(func() {
		r.mu.Lock()
		r.closing = false
		r.mu.Unlock()
	})
	r.touch()
	if s := wsState(t, m, id); s != StateIdle {
		t.Fatalf("touch on a closing runtime made the workspace %s", s)
	}
}
