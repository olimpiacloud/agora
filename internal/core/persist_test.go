package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ageCursor(m *Manager, id string, d time.Duration) {
	m.mu.Lock()
	c := m.cursors[id]
	at := time.Now().UTC().Add(-d)
	c.Updated = at
	c.Ended = &at
	m.mu.Unlock()
}

func newCursor(t *testing.T, m *Manager, name, parent string) string {
	t.Helper()
	c, _, err := m.CreateCursor(context.Background(), CreateCursorReq{Name: name, Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func TestPruneFinishedCursors(t *testing.T) {
	m := newTestManager(t)
	old := newCursor(t, m, "old", "")
	fresh := newCursor(t, m, "fresh", "")
	live := newCursor(t, m, "live", "")
	parent := newCursor(t, m, "parent", "")
	mid := newCursor(t, m, "mid", parent)
	leaf := newCursor(t, m, "leaf", mid)
	for _, id := range []string{old, fresh, parent, mid} {
		if err := m.CancelCursor(id, "test", false); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{old, parent, mid} {
		ageCursor(m, id, 48*time.Hour)
	}
	ageCursor(m, live, 48*time.Hour)
	m.mu.Lock()
	m.cursors[live].Ended = nil
	m.mu.Unlock()
	m.pruneCursors(24 * time.Hour)
	m.store.Flush()
	if _, err := m.GetCursor(old); err == nil {
		t.Fatal("old finished cursor was kept")
	}
	if _, err := os.Stat(m.store.path("cursors", old)); !os.IsNotExist(err) {
		t.Fatalf("old cursor file still on disk: %v", err)
	}
	for _, id := range []string{fresh, live, parent, mid, leaf} {
		if _, err := m.GetCursor(id); err != nil {
			t.Fatalf("cursor %s was pruned", id)
		}
	}
	if _, err := os.Stat(m.store.path("cursors", fresh)); err != nil {
		t.Fatalf("fresh cursor file: %v", err)
	}
	if err := m.CancelCursor(leaf, "test", false); err != nil {
		t.Fatal(err)
	}
	ageCursor(m, leaf, 48*time.Hour)
	m.pruneCursors(0)
	if _, err := m.GetCursor(parent); err != nil {
		t.Fatal("retention 0 deleted a cursor")
	}
	m.pruneCursors(24 * time.Hour)
	for _, id := range []string{parent, mid, leaf} {
		if _, err := m.GetCursor(id); err == nil {
			t.Fatalf("cursor %s kept after its tree ended", id)
		}
	}
}

func checkpointJSONs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range ents {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			out[name] = true
		}
	}
	return out
}

func TestCheckpointPruning(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	w, err := m.CreateWorkspace(ctx, CreateWorkspaceReq{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.cfg.Home, "checkpoints", w.ID)
	snap, err := m.Checkpoint(ctx, w.ID, "snap", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, snap.ID), 0700); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 25; i++ {
		cp, err := m.Checkpoint(ctx, w.ID, "test", false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cp.ID)
	}
	got := checkpointJSONs(t, dir)
	if !got[snap.ID] {
		t.Fatal("checkpoint with a snapshot was pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, snap.ID)); err != nil {
		t.Fatalf("snapshot dir: %v", err)
	}
	if len(got) != keepCheckpoints+1 {
		t.Fatalf("%d checkpoint files, want %d", len(got), keepCheckpoints+1)
	}
	for _, id := range ids[len(ids)-keepCheckpoints:] {
		if !got[id] {
			t.Fatalf("recent checkpoint %s was pruned", id)
		}
	}
}

func TestEventLogRotation(t *testing.T) {
	prev := auditMaxBytes
	auditMaxBytes = 1024
	dir := t.TempDir()
	b := NewBus(dir, 16)
	auditMaxBytes = prev
	p := filepath.Join(dir, "ws.jsonl")
	for i := 0; i < 50; i++ {
		b.Publish("test.event", "ws", "", map[string]any{"i": i, "pad": strings.Repeat("x", 64)})
	}
	fi, err := os.Stat(p + ".1")
	if err != nil {
		t.Fatalf("no rotated log: %v", err)
	}
	if fi.Size() <= 1024 || fi.Size() > 2048 {
		t.Fatalf("rotated log size %d", fi.Size())
	}
	cur, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Size() > 1024 {
		t.Fatalf("active log size %d over the limit", cur.Size())
	}
	b.CloseLog("ws")
	b.Publish("test.event", "ws", "", nil)
	if after, _ := os.Stat(p); after.Size() <= cur.Size() {
		t.Fatal("log not reopened in append mode after close")
	}
}

func TestPurgeRemovesEventLog(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	w, err := m.CreateWorkspace(ctx, CreateWorkspaceReq{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(m.cfg.Home, "events", w.ID+".jsonl")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("event log not created: %v", err)
	}
	if err := m.Destroy(ctx, w.ID, true); err != nil {
		t.Fatal(err)
	}
	m.Bus.Publish("test.late", w.ID, "", nil)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("purged workspace log recreated: %v", err)
	}
	m.Bus.mu.Lock()
	open := m.Bus.logs[w.ID] != nil
	m.Bus.mu.Unlock()
	if open {
		t.Fatal("purged workspace log handle still open")
	}
	m.store.Flush()
	if _, err := os.Stat(m.store.path("workspaces", w.ID)); !os.IsNotExist(err) {
		t.Fatalf("purged workspace state still on disk: %v", err)
	}
}

func TestSuspendReleasesLogAndCPUSample(t *testing.T) {
	m := newTestManager(t)
	id, _ := newTestRuntime(t, m)
	m.mu.Lock()
	m.cpuPrev[id] = cpuSample{t: time.Now()}
	m.mu.Unlock()
	m.Bus.Publish("test.event", id, "", nil)
	if err := m.Suspend(context.Background(), id, "test"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, sampled := m.cpuPrev[id]
	m.mu.Unlock()
	m.Bus.mu.Lock()
	open := m.Bus.logs[id] != nil
	m.Bus.mu.Unlock()
	if sampled || open {
		t.Fatalf("after suspend: cpu sample %v, log open %v", sampled, open)
	}
}

func TestPruneCursorsReleasesEventLogs(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	idle, err := m.CreateWorkspace(ctx, CreateWorkspaceReq{Name: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	purged, err := m.CreateWorkspace(ctx, CreateWorkspaceReq{Name: "purged"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, ws := range []string{idle.ID, purged.ID} {
		c, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "c", Workspace: ws})
		if err != nil {
			t.Fatal(err)
		}
		m.CancelCursor(c.ID, "test", false)
		ids = append(ids, c.ID)
	}
	if err := m.Destroy(ctx, purged.ID, true); err != nil {
		t.Fatal(err)
	}
	m.Bus.mu.Lock()
	delete(m.Bus.gone, purged.ID)
	m.Bus.mu.Unlock()
	for _, id := range ids {
		ageCursor(m, id, 48*time.Hour)
	}
	m.pruneCursors(24 * time.Hour)
	m.Bus.mu.Lock()
	idleOpen := m.Bus.logs[idle.ID] != nil
	purgedOpen := m.Bus.logs[purged.ID] != nil
	m.Bus.mu.Unlock()
	if idleOpen || purgedOpen {
		t.Fatalf("log handles left open: idle %v purged %v", idleOpen, purgedOpen)
	}
	if _, err := os.Stat(filepath.Join(m.cfg.Home, "events", purged.ID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("pruning recreated the purged workspace log: %v", err)
	}
}
