package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func forceRemoveAll(t *testing.T, m *Manager) {
	t.Cleanup(func() {
		for _, wt := range m.ListWorktrees() {
			m.RemoveWorktree(context.Background(), wt.ID, true)
		}
	})
}

func TestDeleteCursorReclaimsCleanWorktree(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	repo := gitRepo(t, filepath.Join(t.TempDir(), "r"))
	id := newCursor(t, m, "agent", "")
	wt, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, id, "")
	if err != nil {
		t.Fatal(err)
	}
	forceRemoveAll(t, m)
	if err := m.DeleteCursor(id); err != nil {
		t.Fatal(err)
	}
	m.store.Flush()
	mustBeGone(t, wt.Path)
	if n := len(m.ListWorktrees()); n != 0 {
		t.Fatalf("%d worktrees listed", n)
	}
	mustBeGone(t, m.store.path("worktrees", wt.ID))
	if err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+wt.Branch).Run(); err != nil {
		t.Fatalf("branch %s gone: %v", wt.Branch, err)
	}
	if !m.holdPorts(wt.PortBase) {
		t.Fatal("port block still taken")
	}
	m.releasePorts(wt.PortBase)
}

func TestDeleteCursorKeepsDirtyWorktree(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	repo := gitRepo(t, filepath.Join(t.TempDir(), "r"))
	id := newCursor(t, m, "agent", "")
	wt, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, id, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.RemoveWorktree(ctx, wt.ID, true) })
	notes := filepath.Join(wt.Path, "notes.txt")
	if err := os.WriteFile(notes, []byte("work"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteCursor(id); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(notes)
	if err != nil || string(b) != "work" {
		t.Fatalf("uncommitted work lost: %q %v", b, err)
	}
	listed := false
	for _, w := range m.ListWorktrees() {
		if w.ID == wt.ID {
			listed = true
		}
	}
	if !listed {
		t.Fatal("dirty worktree record removed")
	}
	if m.holdPorts(wt.PortBase) {
		m.releasePorts(wt.PortBase)
		t.Fatal("dirty worktree's port block freed")
	}
	ev := m.Bus.Recent(Filter{Types: []string{"worktree.kept"}}, 5)
	if len(ev) != 1 {
		t.Fatalf("%d worktree.kept events", len(ev))
	}
	if got := ev[0].Data["id"]; got != wt.ID {
		t.Fatalf("worktree.kept for %v, want %s", got, wt.ID)
	}
}

func TestDeleteCursorLeavesOtherWorktrees(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	repo := gitRepo(t, filepath.Join(t.TempDir(), "r"))
	a := newCursor(t, m, "a", "")
	b := newCursor(t, m, "b", "")
	if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, a, ""); err != nil {
		t.Fatal(err)
	}
	other, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, b, "")
	if err != nil {
		t.Fatal(err)
	}
	forceRemoveAll(t, m)
	if err := m.DeleteCursor(a); err != nil {
		t.Fatal(err)
	}
	mustExist(t, other.Path)
	for _, w := range m.ListWorktrees() {
		if w.ID == other.ID {
			return
		}
	}
	t.Fatal("other cursor's worktree no longer listed")
}

func TestWorktreeLimitPerTree(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	repo := gitRepo(t, filepath.Join(t.TempDir(), "r"))
	root := newCursor(t, m, "root", "")
	child := newCursor(t, m, "child", root)
	forceRemoveAll(t, m)
	var ids []string
	for i := 0; i < maxTreeWorktrees; i++ {
		owner := root
		if i%2 == 1 {
			owner = child
		}
		wt, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, owner, "")
		if err != nil {
			t.Fatalf("worktree %d: %v", i, err)
		}
		ids = append(ids, wt.ID)
	}
	if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, child, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("11th worktree: %v, want forbidden", err)
	}
	other := newCursor(t, m, "other", "")
	if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, other, ""); err != nil {
		t.Fatalf("other tree: %v", err)
	}
	if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo, Trusted: true}, child, ""); err != nil {
		t.Fatalf("trusted: %v", err)
	}
	for _, id := range ids[:2] {
		if err := m.RemoveWorktree(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, child, ""); err != nil {
		t.Fatalf("after removing two: %v", err)
	}
}

func TestWorktreeLimitHoldsUnderConcurrency(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	repo := gitRepo(t, filepath.Join(t.TempDir(), "r"))
	id := newCursor(t, m, "agent", "")
	forceRemoveAll(t, m)
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, id, "")
		}()
	}
	wg.Wait()
	if n := len(m.ListWorktrees()); n > maxTreeWorktrees {
		t.Fatalf("%d worktrees, limit %d", n, maxTreeWorktrees)
	}
}
