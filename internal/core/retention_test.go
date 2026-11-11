package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ageWorkspace(m *Manager, id string, d time.Duration) {
	m.mu.Lock()
	m.ws[id].Updated = time.Now().UTC().Add(-d)
	m.mu.Unlock()
}

func workspaceWithData(t *testing.T, m *Manager, req CreateWorkspaceReq) *Workspace {
	t.Helper()
	w, err := m.CreateWorkspace(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range workspaceDirs(m, w) {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(m.profileDir(w.Config.Profile), "Preferences"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	return w
}

func workspaceDirs(m *Manager, w *Workspace) []string {
	return []string{m.profileDir(w.Config.Profile), filepath.Join(m.wsDir(w.ID), "home"), filepath.Join(m.cfg.Home, "checkpoints", w.ID)}
}

func mustExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
}

func mustBeGone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("%s still exists: %v", p, err)
	}
}

func TestPruneDestroyedWorkspaces(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	old := workspaceWithData(t, m, CreateWorkspaceReq{Name: "old"})
	fresh := workspaceWithData(t, m, CreateWorkspaceReq{Name: "fresh"})
	live := workspaceWithData(t, m, CreateWorkspaceReq{Name: "live"})
	for _, id := range []string{old.ID, fresh.ID} {
		if err := m.Destroy(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	ageWorkspace(m, old.ID, 48*time.Hour)
	ageWorkspace(m, live.ID, 48*time.Hour)
	m.pruneWorkspaces(24 * time.Hour)
	m.store.Flush()
	if _, err := m.GetWorkspace(old.ID); err != ErrNotFound {
		t.Fatalf("old workspace not purged: %v", err)
	}
	for _, d := range workspaceDirs(m, old) {
		mustBeGone(t, d)
	}
	mustBeGone(t, m.store.path("workspaces", old.ID))
	if s := wsState(t, m, fresh.ID); s != StateDestroyed {
		t.Fatalf("fresh workspace is %s", s)
	}
	mustExist(t, m.profileDir(fresh.Config.Profile))
	if s := wsState(t, m, live.ID); s == StateDestroyed {
		t.Fatalf("live workspace is %s", s)
	}
	for _, d := range workspaceDirs(m, live) {
		mustExist(t, d)
	}
	mustExist(t, m.store.path("workspaces", live.ID))
}

func TestPruneKeepsSharedProfile(t *testing.T) {
	m := newTestManager(t)
	a := workspaceWithData(t, m, CreateWorkspaceReq{Name: "a"})
	b, err := m.CreateWorkspace(context.Background(), CreateWorkspaceReq{Name: "b", Config: WorkspaceConfig{Profile: a.Config.Profile}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(context.Background(), a.ID, false); err != nil {
		t.Fatal(err)
	}
	ageWorkspace(m, a.ID, 48*time.Hour)
	m.pruneWorkspaces(24 * time.Hour)
	if _, err := m.GetWorkspace(a.ID); err != ErrNotFound {
		t.Fatalf("a not purged: %v", err)
	}
	mustExist(t, m.profileDir(a.Config.Profile))
	if _, err := m.GetWorkspace(b.ID); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceRetentionZeroKeepsEverything(t *testing.T) {
	m := newTestManager(t)
	w := workspaceWithData(t, m, CreateWorkspaceReq{Name: "w"})
	if err := m.Destroy(context.Background(), w.ID, false); err != nil {
		t.Fatal(err)
	}
	ageWorkspace(m, w.ID, 1000*time.Hour)
	m.pruneWorkspaces(0)
	if s := wsState(t, m, w.ID); s != StateDestroyed {
		t.Fatalf("workspace is %s", s)
	}
	mustExist(t, m.profileDir(w.Config.Profile))
}

func TestDeleteCursorRemovesAgentDir(t *testing.T) {
	m := newTestManager(t)
	id := newCursor(t, m, "agent", "")
	dir := filepath.Join(m.cfg.Home, "agents", id)
	other := filepath.Join(m.cfg.Home, "agents", "cur_other")
	for _, d := range []string{dir, other} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "agent.log"), []byte("log"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.DeleteCursor(id); err != nil {
		t.Fatal(err)
	}
	mustBeGone(t, dir)
	mustExist(t, filepath.Join(other, "agent.log"))
}
