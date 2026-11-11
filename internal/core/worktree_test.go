package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/olimpiacloud/agora/internal/stream"
)

func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func TestWorktreeRootsLimitUntrustedRepos(t *testing.T) {
	t.Setenv("AGORA_CGROUPS", "off")
	roots := t.TempDir()
	m, err := NewManager(Config{Home: t.TempDir(), RTC: stream.RTCOptions{IncludeLoop: true}, WorktreeRoots: []string{" " + roots + "/ "}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	ctx := context.Background()
	inside := gitRepo(t, filepath.Join(roots, "inside"))
	outside := gitRepo(t, filepath.Join(t.TempDir(), "outside"))
	link := filepath.Join(roots, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(inside, "sub"), 0755)
	for _, repo := range []string{outside, link, filepath.Join(roots, "inside", "..", "..", filepath.Base(filepath.Dir(outside)), "outside")} {
		if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, "", ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("untrusted worktree of %s: got %v, want forbidden", repo, err)
		}
	}
	wt, err := m.CreateWorktree(ctx, WorktreeReq{Repo: filepath.Join(inside, "sub")}, "", "")
	if err != nil {
		t.Fatalf("untrusted worktree inside the roots: %v", err)
	}
	t.Cleanup(func() { m.RemoveWorktree(ctx, wt.ID, true) })
	if real, _ := filepath.EvalSymlinks(inside); wt.Repo != real {
		t.Fatalf("worktree repo %s, want %s", wt.Repo, real)
	}
	wt2, err := m.CreateWorktree(ctx, WorktreeReq{Repo: link, Trusted: true}, "", "")
	if err != nil {
		t.Fatalf("trusted worktree outside the roots: %v", err)
	}
	t.Cleanup(func() { m.RemoveWorktree(ctx, wt2.ID, true) })
	if _, err := NewManager(Config{Home: t.TempDir(), WorktreeRoots: []string{"relative/dir"}}); err == nil {
		t.Fatal("relative worktree root accepted")
	}
}

func TestWorktreeRefusesReposNotOwnedByDaemon(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	repo := gitRepo(t, filepath.Join(t.TempDir(), "r"))
	gitFile := t.TempDir()
	os.WriteFile(filepath.Join(gitFile, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git")+"\n"), 0644)
	if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: gitFile}, "", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("repo with a .git file: got %v, want forbidden", err)
	}
	if os.Geteuid() != 0 {
		t.Skip("ownership refusal needs root to chown")
	}
	for _, p := range []string{filepath.Join(repo, ".git", "config"), filepath.Join(repo, ".git"), repo} {
		if err := os.Chown(p, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if _, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, "", ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("repo with %s owned by nobody: got %v, want forbidden", p, err)
		}
		os.Chown(p, 0, 0)
	}
	wt, err := m.CreateWorktree(ctx, WorktreeReq{Repo: repo}, "", "")
	if err != nil {
		t.Fatalf("root-owned repo: %v", err)
	}
	m.RemoveWorktree(ctx, wt.ID, true)
}
