package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type WorktreeReq struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	Ports  int    `json:"ports,omitempty"`
}

var branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func portsFree(base, n int) bool {
	for p := base; p < base+n; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return false
		}
		l.Close()
	}
	return true
}

func (m *Manager) allocPorts(n int) int {
	m.mu.Lock()
	used := map[int]bool{}
	for _, w := range m.worktrees {
		used[w.PortBase] = true
	}
	m.mu.Unlock()
	for base := 21000; base < 40000; base += 20 {
		if !used[base] && portsFree(base, n) {
			return base
		}
	}
	return 0
}

func (m *Manager) CreateWorktree(ctx context.Context, req WorktreeReq, cursor, ws string) (*Worktree, error) {
	repo, err := filepath.Abs(req.Repo)
	if err != nil {
		return nil, err
	}
	top, err := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("%s is not a git repository", repo)
	}
	repo = strings.TrimSpace(string(top))
	id := NewID("wt")
	if req.Branch == "" {
		req.Branch = "agora/" + strings.TrimPrefix(id, "wt_")
	}
	if !branchRe.MatchString(req.Branch) || strings.Contains(req.Branch, "..") || strings.HasPrefix(req.Branch, "-") {
		return nil, fmt.Errorf("invalid branch name")
	}
	if req.Base == "" {
		req.Base = "HEAD"
	}
	if strings.HasPrefix(req.Base, "-") {
		return nil, fmt.Errorf("invalid base")
	}
	if req.Ports == 0 {
		req.Ports = 10
	}
	path := filepath.Join(m.cfg.Home, "worktrees", id)
	args := []string{"-C", repo, "worktree", "add", "-b", req.Branch, path, req.Base}
	if exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+req.Branch).Run() == nil {
		args = []string{"-C", repo, "worktree", "add", path, req.Branch}
	}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	wt := &Worktree{ID: id, Repo: repo, Path: path, Branch: req.Branch, Base: req.Base, Cursor: cursor, Workspace: ws, PortBase: m.allocPorts(req.Ports), Ports: req.Ports, Created: time.Now().UTC()}
	m.mu.Lock()
	m.worktrees[id] = wt
	m.store.Put("worktrees", id, wt)
	m.mu.Unlock()
	m.Bus.Publish("worktree.created", ws, cursor, map[string]any{"id": id, "branch": wt.Branch, "path": path, "port_base": wt.PortBase})
	return wt, nil
}

func (m *Manager) ListWorktrees() []*Worktree {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*Worktree{}
	for _, w := range m.worktrees {
		cp := *w
		out = append(out, &cp)
	}
	return out
}

func (m *Manager) RemoveWorktree(ctx context.Context, id string, force bool) error {
	m.mu.Lock()
	wt := m.worktrees[id]
	m.mu.Unlock()
	if wt == nil {
		return ErrNotFound
	}
	args := []string{"-C", wt.Repo, "worktree", "remove", wt.Path}
	if force {
		args = append(args, "--force")
	}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		if _, serr := os.Stat(wt.Path); serr == nil {
			return fmt.Errorf("git worktree remove: %v: %s", err, strings.TrimSpace(string(out)))
		}
		exec.CommandContext(ctx, "git", "-C", wt.Repo, "worktree", "prune").Run()
	}
	m.mu.Lock()
	delete(m.worktrees, id)
	m.store.Delete("worktrees", id)
	m.mu.Unlock()
	m.Bus.Publish("worktree.removed", wt.Workspace, wt.Cursor, map[string]any{"id": id, "branch": wt.Branch})
	return nil
}
