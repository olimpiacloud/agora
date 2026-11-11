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
