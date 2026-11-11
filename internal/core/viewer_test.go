package core

import "testing"

func TestControlViewerNoopTakesNoLock(t *testing.T) {
	m := &Manager{Arb: NewArbiter(nil), ws: map[string]*Workspace{}}
	r := &Runtime{m: m, id: "ws1"}
	v := &Viewer{ID: "v1", Workspace: "ws1", Control: true, r: r, m: m, held: map[uint32]bool{}, buttons: map[int]bool{}}
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

func TestViewerButtons(t *testing.T) {
	for in, want := range map[int]int{0: 1, 1: 2, 2: 3, 3: 8, 4: 9, 5: 0, -1: 0, 1000: 0} {
		if got := btn(in); got != want {
			t.Errorf("btn(%d) = %d, want %d", in, got, want)
		}
	}
}
