package core

import (
	"context"
	"fmt"
	"testing"
)

func TestShotsAreLimitedPerWorkspace(t *testing.T) {
	var s shotStore
	keep := &Shot{ID: "keep", Workspace: "b", Data: []byte("x")}
	s.put(keep)
	for i := 0; i < shotsPerWorkspace+10; i++ {
		s.put(&Shot{ID: fmt.Sprint("a", i), Workspace: "a", Data: []byte("png")})
	}
	if s.get("keep") != keep {
		t.Fatal("a busy workspace evicted another workspace's shot")
	}
	if len(s.ws["a"]) != shotsPerWorkspace || s.get("a9") != nil || s.get("a10") == nil {
		t.Fatalf("workspace a keeps %d shots, want the newest %d", len(s.ws["a"]), shotsPerWorkspace)
	}
	big := make([]byte, shotBytesPerWorkspace/2+1)
	s.put(&Shot{ID: "big1", Workspace: "b", Data: big})
	s.put(&Shot{ID: "big2", Workspace: "b", Data: big})
	if s.get("big1") != nil || s.get("keep") != nil || s.get("big2") == nil {
		t.Fatal("per-workspace byte limit not enforced")
	}
	if s.get("a10") == nil {
		t.Fatal("byte eviction in b touched a")
	}
	huge := make([]byte, shotBytesPerWorkspace+1)
	s.put(&Shot{ID: "huge", Workspace: "c", Data: huge})
	if s.get("huge") == nil {
		t.Fatal("a single oversized shot must still be kept")
	}
}

func TestShotsGlobalCapEvictsOldestAcrossWorkspaces(t *testing.T) {
	var s shotStore
	data := make([]byte, shotBytesPerWorkspace)
	n := shotBytesTotal/shotBytesPerWorkspace + 1
	for i := 0; i <= n; i++ {
		s.put(&Shot{ID: fmt.Sprint("s", i), Workspace: fmt.Sprint("w", i), Data: data})
	}
	if s.bytes > shotBytesTotal {
		t.Fatalf("store holds %d bytes, cap %d", s.bytes, shotBytesTotal)
	}
	if s.get("s0") != nil || s.get("s1") != nil {
		t.Fatal("oldest shots not evicted first")
	}
	for i := 2; i <= n; i++ {
		if s.get(fmt.Sprint("s", i)) == nil {
			t.Fatalf("shot s%d evicted, want only the oldest gone", i)
		}
	}
	if _, ok := s.ws["w0"]; ok {
		t.Fatal("empty workspace entry left behind")
	}
}

func TestShotsDroppedOnSuspendAndDestroy(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	a, err := m.CreateWorkspace(ctx, CreateWorkspaceReq{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateWorkspace(ctx, CreateWorkspaceReq{Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	sa := m.StoreShot(a.ID, "", "image/png", []byte("a"))
	sb := m.StoreShot(b.ID, "", "image/png", []byte("bb"))
	if err := m.Suspend(ctx, a.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if m.GetShot(sa) != nil {
		t.Fatal("shot survived suspend")
	}
	if m.GetShot(sb) == nil {
		t.Fatal("suspending a dropped another workspace's shot")
	}
	if err := m.Destroy(ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if m.GetShot(sb) != nil {
		t.Fatal("shot survived destroy")
	}
	if m.shots.bytes != 0 {
		t.Fatalf("store still counts %d bytes", m.shots.bytes)
	}
}
