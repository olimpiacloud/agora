package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUploadPathsStayInWorkspaceHome(t *testing.T) {
	m := newTestManager(t)
	home := filepath.Join(m.wsDir("ws_up"), "home")
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(home, "Downloads", "a.txt")
	os.WriteFile(inside, []byte("a"), 0600)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("s"), 0600)
	os.Symlink(outside, filepath.Join(home, "link"))
	got, err := m.uploadPaths("ws_up", []string{inside, "Downloads/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(inside)
	if got[0] != real || got[1] != real {
		t.Fatalf("resolved paths: %v", got)
	}
	for _, bad := range []string{outside, filepath.Join(home, "link"), filepath.Join(home, "..", "..", "..", "admin.token"), "../../x", home} {
		if _, err := m.uploadPaths("ws_up", []string{bad}); err == nil {
			t.Fatalf("%s: want an error", bad)
		} else if _, statErr := os.Stat(bad); statErr == nil && !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: got %v, want forbidden", bad, err)
		}
	}
}

func TestWorktreeSkipsRepositoryHooks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	m := newTestManager(t)
	repo := t.TempDir()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0755)
	wt, err := m.CreateWorktree(context.Background(), WorktreeReq{Repo: repo}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.RemoveWorktree(context.Background(), wt.ID, true) })
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("post-checkout hook ran as the daemon")
	}
}
