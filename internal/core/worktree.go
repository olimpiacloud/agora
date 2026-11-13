package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type WorktreeReq struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	Ports  int    `json:"ports,omitempty"`

	Trusted bool `json:"-"`
}

var branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func gitCmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
}

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

func (m *Manager) allocPorts(n int) (int, error) {
	for base := 21000; base < 40000; base += 20 {
		if !m.holdPorts(base) {
			continue
		}
		if portsFree(base, n) {
			return base, nil
		}
		m.releasePorts(base)
	}
	return 0, errors.New("no free port block for the worktree")
}

func (m *Manager) holdPorts(base int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.portsHeld[base] {
		return false
	}
	for _, w := range m.worktrees {
		if w.PortBase == base {
			return false
		}
	}
	m.portsHeld[base] = true
	return true
}

func (m *Manager) releasePorts(base int) {
	m.mu.Lock()
	delete(m.portsHeld, base)
	m.mu.Unlock()
}

const maxTreeWorktrees = 10

func (m *Manager) reserveWorktree(root string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.wtPending[root]
	for _, w := range m.worktrees {
		r := w.Root
		if r == "" {
			if c := m.cursors[w.Cursor]; c != nil {
				r = c.Root
			}
		}
		if r == root {
			n++
		}
	}
	if n >= maxTreeWorktrees {
		return fmt.Errorf("%w: cursor tree already has %d worktrees (limit %d); delete finished subagents or ask the admin to remove worktrees", ErrForbidden, n, maxTreeWorktrees)
	}
	if m.wtPending == nil {
		m.wtPending = map[string]int{}
	}
	m.wtPending[root]++
	return nil
}

func (m *Manager) unreserveWorktree(root string) {
	m.mu.Lock()
	m.wtPending[root]--
	if m.wtPending[root] <= 0 {
		delete(m.wtPending, root)
	}
	m.mu.Unlock()
}

func (m *Manager) CreateWorktree(ctx context.Context, req WorktreeReq, cursor, ws string) (*Worktree, error) {
	if req.Ports < 0 || req.Ports > 20 {
		return nil, fmt.Errorf("ports must be between 1 and 20 (0 keeps the default of 10)")
	}
	repo, err := filepath.Abs(req.Repo)
	if err != nil {
		return nil, err
	}
	checked := ""
	if !req.Trusted {
		if checked, err = m.allowedRepo(repo); err != nil {
			return nil, err
		}
		repo = checked
	}
	top, err := gitCmd(ctx, "-C", repo, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("%s is not a git repository", repo)
	}
	repo = strings.TrimSpace(string(top))
	if checked != "" && repo != checked {
		return nil, fmt.Errorf("%w: %s resolves to a different repository than checked", ErrForbidden, req.Repo)
	}
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
	root := ""
	m.mu.Lock()
	if c := m.cursors[cursor]; c != nil {
		root = c.Root
	}
	m.mu.Unlock()
	limited := !req.Trusted && root != ""
	if limited {
		if err := m.reserveWorktree(root); err != nil {
			return nil, err
		}
	}
	base, err := m.allocPorts(req.Ports)
	if err != nil {
		if limited {
			m.unreserveWorktree(root)
		}
		return nil, err
	}
	path := filepath.Join(m.cfg.Home, "worktrees", id)
	args := []string{"-C", repo, "worktree", "add", "-b", req.Branch, path, req.Base}
	if gitCmd(ctx, "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+req.Branch).Run() == nil {
		args = []string{"-C", repo, "worktree", "add", path, req.Branch}
	}
	if out, err := gitCmd(ctx, args...).CombinedOutput(); err != nil {
		m.releasePorts(base)
		if limited {
			m.unreserveWorktree(root)
		}
		return nil, fmt.Errorf("git worktree add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	wt := &Worktree{ID: id, Repo: repo, Path: path, Branch: req.Branch, Base: req.Base, Cursor: cursor, Workspace: ws, Root: root, PortBase: base, Ports: req.Ports, Created: time.Now().UTC()}
	m.mu.Lock()
	m.worktrees[id] = wt
	delete(m.portsHeld, base)
	if limited {
		m.wtPending[root]--
		if m.wtPending[root] <= 0 {
			delete(m.wtPending, root)
		}
	}
	m.store.Put("worktrees", id, wt)
	m.mu.Unlock()
	m.Bus.Publish("worktree.created", ws, cursor, map[string]any{"id": id, "branch": wt.Branch, "path": path, "port_base": wt.PortBase})
	return wt, nil
}

func (m *Manager) allowedRepo(repo string) (string, error) {
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository", repo)
	}
	if len(m.cfg.WorktreeRoots) > 0 && !underRoots(resolved, m.cfg.WorktreeRoots) {
		return "", fmt.Errorf("%w: %s is outside the allowed worktree roots (agora serve --worktree-roots)", ErrForbidden, resolved)
	}
	top := resolved
	for {
		if _, err := os.Lstat(filepath.Join(top, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(top)
		if parent == top {
			return "", fmt.Errorf("%s is not a git repository", resolved)
		}
		top = parent
	}
	if len(m.cfg.WorktreeRoots) > 0 && !underRoots(top, m.cfg.WorktreeRoots) {
		return "", fmt.Errorf("%w: repository %s is outside the allowed worktree roots (agora serve --worktree-roots)", ErrForbidden, top)
	}
	uid := os.Geteuid()
	gitDir := filepath.Join(top, ".git")
	for _, p := range []string{top, gitDir, filepath.Join(gitDir, "config")} {
		fi, err := os.Lstat(p)
		if err != nil {
			if p != top && p != gitDir && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		if p == gitDir && !fi.IsDir() {
			return "", fmt.Errorf("%w: %s must be a directory, not a link or a gitdir file", ErrForbidden, gitDir)
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != uid {
			return "", fmt.Errorf("%w: %s is not owned by uid %d (the user running agora); cursors only get worktrees of repositories it owns", ErrForbidden, p, uid)
		}
	}
	return top, nil
}

func underRoots(path string, roots []string) bool {
	for _, root := range roots {
		if r, err := filepath.EvalSymlinks(root); err == nil {
			root = r
		}
		if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
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

func (m *Manager) reclaimWorktrees(cursor, ws string) {
	var ids []string
	m.mu.Lock()
	for id, w := range m.worktrees {
		if w.Cursor == cursor {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := m.RemoveWorktree(ctx, id, false)
		cancel()
		if err != nil {
			m.Bus.Publish("worktree.kept", ws, cursor, map[string]any{"id": id, "reason": err.Error()})
		}
	}
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
	if out, err := gitCmd(ctx, args...).CombinedOutput(); err != nil {
		if _, serr := os.Stat(wt.Path); serr == nil {
			return fmt.Errorf("git worktree remove: %v: %s", err, strings.TrimSpace(string(out)))
		}
		gitCmd(ctx, "-C", wt.Repo, "worktree", "prune").Run()
	}
	m.mu.Lock()
	delete(m.worktrees, id)
	m.store.Delete("worktrees", id)
	m.mu.Unlock()
	m.Bus.Publish("worktree.removed", wt.Workspace, wt.Cursor, map[string]any{"id": id, "branch": wt.Branch})
	return nil
}
