package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"syscall"
	"time"
)

type CreateCursorReq struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind,omitempty"`
	Parent      string            `json:"parent,omitempty"`
	Workspace   string            `json:"workspace,omitempty"`
	Goal        string            `json:"goal,omitempty"`
	Agent       string            `json:"agent,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Budget      Budget            `json:"budget"`
	Launch      *LaunchSpec       `json:"launch,omitempty"`
	Worktree    *WorktreeReq      `json:"worktree,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

var DefaultPermissions = []string{"desktop", "browser", "process", "spawn", "message"}

func (m *Manager) CreateCursor(ctx context.Context, req CreateCursorReq) (*Cursor, string, error) {
	if req.Kind == "" {
		req.Kind = "agent"
	}
	var parent *Cursor
	depth := 0
	root := ""
	m.mu.Lock()
	if req.Parent != "" {
		parent = m.cursors[req.Parent]
		if parent == nil {
			m.mu.Unlock()
			return nil, "", fmt.Errorf("parent: %w", ErrNotFound)
		}
		if parent.Status.Terminal() {
			m.mu.Unlock()
			return nil, "", fmt.Errorf("parent is %s", parent.Status)
		}
		depth = parent.Depth + 1
		root = parent.Root
		if parent.Budget.MaxDepth < 0 || depth > 8 {
			m.mu.Unlock()
			return nil, "", fmt.Errorf("delegation depth limit reached (depth %d)", depth)
		}
		if parent.Budget.MaxCursors > 0 {
			n := 0
			for _, c := range m.cursors {
				if c.Parent == parent.ID && !c.Status.Terminal() {
					n++
				}
			}
			if n >= parent.Budget.MaxCursors {
				m.mu.Unlock()
				return nil, "", fmt.Errorf("parent child limit reached (%d)", n)
			}
		}
		if req.Workspace == "" {
			req.Workspace = parent.Workspace
		}
		if len(req.Permissions) == 0 {
			req.Permissions = parent.Permissions
		} else {
			req.Permissions = intersect(req.Permissions, parent.Permissions)
			if len(req.Permissions) == 0 {
				m.mu.Unlock()
				return nil, "", fmt.Errorf("%w: parent does not hold any of the requested permissions", ErrForbidden)
			}
		}
		if parent.Budget.MaxDepth > 0 {
			req.Budget.MaxDepth = parent.Budget.MaxDepth - 1
			if req.Budget.MaxDepth == 0 {
				req.Budget.MaxDepth = -1
			}
		}
	}
	if req.Workspace != "" {
		w := m.ws[req.Workspace]
		if w == nil || w.State == StateDestroyed {
			m.mu.Unlock()
			return nil, "", fmt.Errorf("workspace: %w", ErrNotFound)
		}
		if w.Config.Budget.MaxCursors > 0 {
			n := 0
			for _, c := range m.cursors {
				if c.Workspace == w.ID && !c.Status.Terminal() {
					n++
				}
			}
			if n >= w.Config.Budget.MaxCursors {
				m.mu.Unlock()
				return nil, "", fmt.Errorf("workspace cursor limit reached (%d)", n)
			}
		}
	}
	if parent == nil && len(req.Permissions) == 0 {
		req.Permissions = DefaultPermissions
	}
	id := NewID("cur")
	if root == "" {
		root = id
	}
	n := 0
	for _, c := range m.cursors {
		if c.Workspace == req.Workspace {
			n++
		}
	}
	if req.Name == "" {
		req.Name = id
	}
	tok := NewSecret()
	now := time.Now().UTC()
	c := &Cursor{ID: id, Name: req.Name, Kind: req.Kind, Parent: req.Parent, Root: root, Depth: depth, Agent: req.Agent, Goal: req.Goal, Status: CursorPending, Workspace: req.Workspace, Permissions: req.Permissions, Budget: req.Budget, Color: palette[n%len(palette)], Created: now, Updated: now, TokenHash: hashToken(tok), Launch: req.Launch, Labels: req.Labels}
	m.cursors[id] = c
	m.mu.Unlock()
	if req.Worktree != nil {
		wt, err := m.CreateWorktree(ctx, *req.Worktree, id, req.Workspace)
		if err != nil {
			m.mu.Lock()
			delete(m.cursors, id)
			m.mu.Unlock()
			return nil, "", err
		}
		m.mu.Lock()
		c.Worktree = wt.ID
		m.mu.Unlock()
	}
	m.mu.Lock()
	m.tokens[c.TokenHash] = id
	m.saveCursor(c)
	cp := *c
	m.mu.Unlock()
	m.Bus.Publish("cursor.created", c.Workspace, id, map[string]any{"name": c.Name, "parent": c.Parent, "goal": c.Goal, "kind": c.Kind, "color": c.Color, "depth": depth})
	if req.Launch != nil {
		if err := m.launchAgent(c.ID, tok); err != nil {
			m.setCursorStatus(c.ID, CursorFailed, err.Error(), true)
			return &cp, tok, err
		}
	}
	return &cp, tok, nil
}

func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range b {
		set[x] = true
	}
	out := []string{}
	for _, x := range a {
		if set[x] || set["*"] {
			out = append(out, x)
		}
	}
	return out
}

func (c *Cursor) Can(p string) bool {
	for _, x := range c.Permissions {
		if x == p || x == "*" {
			return true
		}
	}
	return false
}

func (m *Manager) GetCursor(id string) (*Cursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cursors[id]
	if c == nil {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}

type CursorQuery struct {
	Workspace string
	Parent    string
	Root      string
	Active    bool
}

func (m *Manager) ListCursors(q CursorQuery) []*Cursor {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*Cursor{}
	for _, c := range m.cursors {
		if q.Workspace != "" && c.Workspace != q.Workspace {
			continue
		}
		if q.Parent != "" && c.Parent != q.Parent {
			continue
		}
		if q.Root != "" && c.Root != q.Root {
			continue
		}
		if q.Active && c.Status.Terminal() {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (m *Manager) IsAncestor(anc, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ancestorLocked(anc, id)
}

func (m *Manager) ancestorLocked(anc, id string) bool {
	for i := 0; i < 32 && id != ""; i++ {
		if id == anc {
			return true
		}
		c := m.cursors[id]
		if c == nil {
			return false
		}
		id = c.Parent
	}
	return false
}

func (m *Manager) setCursorStatus(id string, st CursorStatus, detail string, force bool) error {
	return m.setCursorStatusWith(id, st, detail, force, nil)
}

func (m *Manager) setCursorStatusWith(id string, st CursorStatus, detail string, force bool, set func(*Cursor)) error {
	m.mu.Lock()
	c := m.cursors[id]
	if c == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	if c.Status.Terminal() && !force {
		m.mu.Unlock()
		return fmt.Errorf("cursor already %s", c.Status)
	}
	prev := c.Status
	if set != nil {
		set(c)
	}
	c.Status = st
	c.Detail = detail
	if st.Terminal() && c.Ended == nil {
		now := time.Now().UTC()
		c.Ended = &now
	}
	m.saveCursor(c)
	ws := c.Workspace
	m.mu.Unlock()
	if prev == CursorPaused && st != CursorPaused {
		m.signalLaunch(id, false)
	}
	m.Bus.Publish("cursor.status", ws, id, map[string]any{"from": string(prev), "to": string(st), "detail": detail})
	if st.Terminal() {
		m.Bus.Publish("cursor.ended", ws, id, map[string]any{"status": string(st)})
		m.cleanupCursor(id)
	}
	return nil
}

func (m *Manager) SetStatus(id string, st CursorStatus, detail string) error {
	switch st {
	case CursorRunning, CursorThinking, CursorExecuting, CursorWaiting, CursorBlocked, CursorNeedHuman, CursorPending:
	default:
		return fmt.Errorf("use complete/fail/cancel/pause for status %q", st)
	}
	return m.setCursorStatus(id, st, detail, false)
}

func (m *Manager) Report(id, msg string, pct int) error {
	m.mu.Lock()
	c := m.cursors[id]
	if c == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	c.Progress = append(c.Progress, Progress{Time: time.Now().UTC(), Message: msg, Percent: pct})
	if len(c.Progress) > 50 {
		c.Progress = c.Progress[len(c.Progress)-50:]
	}
	if c.Status == CursorPending {
		c.Status = CursorRunning
	}
	m.saveCursor(c)
	ws := c.Workspace
	m.mu.Unlock()
	m.Bus.Publish("cursor.progress", ws, id, map[string]any{"message": msg, "percent": pct})
	return nil
}

func (m *Manager) Complete(id string, result json.RawMessage) error {
	return m.setCursorStatusWith(id, CursorDone, "", false, func(c *Cursor) { c.Result = result })
}

func (m *Manager) Fail(id, msg string) error {
	return m.setCursorStatusWith(id, CursorFailed, msg, false, func(c *Cursor) { c.Error = msg })
}

func (m *Manager) CancelCursor(id, reason string, cascade bool) error {
	err := m.setCursorStatus(id, CursorCancelled, reason, false)
	if cascade {
		for _, ch := range m.ListCursors(CursorQuery{Parent: id, Active: true}) {
			m.CancelCursor(ch.ID, "parent cancelled", true)
		}
	}
	return err
}

func (m *Manager) Pause(id string) error {
	if err := m.setCursorStatus(id, CursorPaused, "", false); err != nil {
		return err
	}
	m.signalLaunch(id, true)
	return nil
}

func (m *Manager) ResumeCursor(id string) error {
	c, err := m.GetCursor(id)
	if err != nil {
		return err
	}
	if c.Status != CursorPaused {
		return fmt.Errorf("cursor is %s", c.Status)
	}
	m.signalLaunch(id, false)
	return m.setCursorStatus(id, CursorRunning, "resumed", false)
}

func (m *Manager) DeleteCursor(id string) error {
	c, err := m.GetCursor(id)
	if err != nil {
		return err
	}
	if !c.Status.Terminal() {
		m.CancelCursor(id, "deleted", true)
	} else {
		m.closeOwned(c.Workspace, id, true)
	}
	m.mu.Lock()
	delete(m.cursors, id)
	delete(m.tokens, c.TokenHash)
	delete(m.mail, id)
	m.mu.Unlock()
	m.store.Delete("cursors", id)
	m.Bus.Publish("cursor.deleted", c.Workspace, id, nil)
	return nil
}

type TransferReq struct {
	Workspace *string `json:"workspace,omitempty"`
	Parent    *string `json:"parent,omitempty"`
	Agent     *string `json:"agent,omitempty"`
}

func (m *Manager) Transfer(id string, t TransferReq) (*Cursor, error) {
	m.mu.Lock()
	c := m.cursors[id]
	if c == nil {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	oldWS := c.Workspace
	if t.Workspace != nil {
		if w := m.ws[*t.Workspace]; w == nil && *t.Workspace != "" {
			m.mu.Unlock()
			return nil, fmt.Errorf("workspace: %w", ErrNotFound)
		}
		c.Workspace = *t.Workspace
		c.Surface = ""
	}
	if t.Parent != nil {
		if p := m.cursors[*t.Parent]; p == nil && *t.Parent != "" {
			m.mu.Unlock()
			return nil, fmt.Errorf("parent: %w", ErrNotFound)
		} else if p != nil && m.ancestorLocked(id, p.ID) {
			m.mu.Unlock()
			return nil, fmt.Errorf("parent %s is %s itself or one of its descendants", p.ID, id)
		} else if p != nil {
			c.Parent = p.ID
			c.Root = p.Root
			c.Depth = p.Depth + 1
		} else {
			c.Parent = ""
			c.Root = c.ID
			c.Depth = 0
		}
	}
	if t.Agent != nil {
		c.Agent = *t.Agent
	}
	m.saveCursor(c)
	cp := *c
	m.mu.Unlock()
	if t.Workspace != nil && oldWS != cp.Workspace {
		m.releaseCursorIn(oldWS, id)
	}
	m.Bus.Publish("cursor.transferred", cp.Workspace, id, map[string]any{"from_workspace": oldWS, "parent": cp.Parent, "agent": cp.Agent})
	return &cp, nil
}

func (m *Manager) releaseCursorIn(ws, id string) {
	if r := m.runtime(ws); r != nil {
		r.mu.Lock()
		if s := r.seats[id]; s != nil {
			delete(r.seats, id)
			go s.seat.Close()
		}
		for t, o := range r.tabOwner {
			if o == id {
				delete(r.tabOwner, t)
			}
		}
		r.mu.Unlock()
	}
}

func (m *Manager) cleanupCursor(id string) {
	c, err := m.GetCursor(id)
	if err != nil {
		return
	}
	for _, l := range m.Arb.ReleaseHolder(id) {
		m.Bus.Publish("control.released", c.Workspace, id, map[string]any{"surface": l.Surface})
	}
	if c.Status == CursorCancelled {
		m.killLaunch(id)
	} else {
		go func() {
			time.Sleep(30 * time.Second)
			m.killLaunch(id)
		}()
	}
	if r := m.runtime(c.Workspace); r != nil {
		r.mu.Lock()
		if s := r.seats[id]; s != nil {
			delete(r.seats, id)
			go s.seat.Close()
		}
		r.mu.Unlock()
	}
	if c.Labels["keep_tabs"] == "true" && c.Status != CursorCancelled {
		return
	}
	m.closeOwned(c.Workspace, id, c.Status == CursorCancelled)
}

func (m *Manager) closeOwned(ws, id string, apps bool) {
	r := m.runtime(ws)
	if r == nil {
		return
	}
	var close []string
	var pids []int
	r.mu.Lock()
	for t, o := range r.tabOwner {
		if o == id {
			close = append(close, t)
			delete(r.tabOwner, t)
		}
	}
	for pid, p := range r.procs {
		if apps && p.App.Owner == id {
			pids = append(pids, pid)
		}
	}
	r.mu.Unlock()
	for _, pid := range pids {
		syscall.Kill(-pid, syscall.SIGTERM)
	}
	if len(close) == 0 {
		return
	}
	r.brMu.Lock()
	b := r.browser
	r.brMu.Unlock()
	if b == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, t := range close {
			if tab := b.Tab(t); tab != nil {
				tab.Close(ctx)
			}
		}
	}()
}

func (m *Manager) SendMessage(from, to string, body json.RawMessage) (*Message, error) {
	m.mu.Lock()
	tc := m.cursors[to]
	if tc == nil {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	m.mailSeq++
	msg := Message{Seq: m.mailSeq, Time: time.Now().UTC(), From: from, To: to, Body: body}
	m.mail[to] = append(m.mail[to], msg)
	if len(m.mail[to]) > 200 {
		m.mail[to] = m.mail[to][len(m.mail[to])-200:]
	}
	ws := tc.Workspace
	m.mu.Unlock()
	m.Bus.Publish("cursor.message", ws, to, map[string]any{"from": from, "seq": msg.Seq})
	return &msg, nil
}

func (m *Manager) Inbox(id string, since int64) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Message{}
	for _, msg := range m.mail[id] {
		if msg.Seq > since {
			out = append(out, msg)
		}
	}
	return out
}

func (m *Manager) WaitCursors(ctx context.Context, ids []string, all bool, timeout time.Duration) ([]*Cursor, error) {
	if timeout <= 0 || timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	ch, unsub := m.Bus.Subscribe(Filter{Types: []string{"cursor.ended"}}, 64)
	defer unsub()
	deadline := time.After(timeout)
	for {
		var out []*Cursor
		done := 0
		for _, id := range ids {
			c, err := m.GetCursor(id)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
			if c.Status.Terminal() {
				done++
			}
		}
		if (all && done == len(ids)) || (!all && done > 0) {
			return out, nil
		}
		select {
		case <-ch:
		case <-deadline:
			return out, errors.New("timeout")
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}
