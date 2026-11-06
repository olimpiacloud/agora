package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

type CreateWorkspaceReq struct {
	Name   string            `json:"name"`
	Owner  string            `json:"owner,omitempty"`
	Config WorkspaceConfig   `json:"config"`
	Start  bool              `json:"start,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (m *Manager) CreateWorkspace(ctx context.Context, req CreateWorkspaceReq) (*Workspace, error) {
	id := NewID("ws")
	if req.Name == "" {
		req.Name = id
	}
	if req.Config.Width == 0 {
		req.Config.Width, req.Config.Height = m.cfg.Width, m.cfg.Height
	}
	if req.Config.Width > 3840 || req.Config.Height > 2160 || req.Config.Width < 320 || req.Config.Height < 240 {
		return nil, fmt.Errorf("resolution out of range")
	}
	if req.Config.Layout == "" {
		req.Config.Layout = "tile"
	}
	if req.Config.IdleAfter.Duration == 0 {
		req.Config.IdleAfter.Duration = m.cfg.IdleAfter
	}
	if req.Config.SuspendAfter.Duration == 0 {
		req.Config.SuspendAfter.Duration = m.cfg.SuspendAfter
	}
	if req.Config.Profile == "" {
		req.Config.Profile = id
	}
	now := time.Now().UTC()
	w := &Workspace{ID: id, Name: req.Name, State: StateCreated, Config: req.Config, Owner: req.Owner, Created: now, Updated: now, LastActive: now, Labels: req.Labels}
	m.mu.Lock()
	m.ws[id] = w
	m.saveWS(w)
	m.mu.Unlock()
	m.Bus.Publish("workspace.created", id, req.Owner, map[string]any{"name": w.Name, "profile": w.Config.Profile})
	if req.Start {
		if _, err := m.ensureRuntime(ctx, id, true); err != nil {
			return w, err
		}
	}
	return m.GetWorkspace(id)
}

func (m *Manager) GetWorkspace(id string) (*Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.ws[id]
	if w == nil {
		return nil, ErrNotFound
	}
	cp := *w
	return &cp, nil
}

type WorkspaceView struct {
	Workspace
	Display   string          `json:"display,omitempty"`
	Browser   bool            `json:"browser"`
	Viewers   int             `json:"viewers"`
	Seats     int             `json:"seats"`
	Processes int             `json:"processes"`
	Cursors   int             `json:"cursors"`
	Stream    any             `json:"stream,omitempty"`
	Usage     *WorkspaceUsage `json:"usage,omitempty"`
	Locks     []Lock          `json:"locks"`
}

func (m *Manager) ViewWorkspace(id string) (*WorkspaceView, error) {
	w, err := m.GetWorkspace(id)
	if err != nil {
		return nil, err
	}
	v := &WorkspaceView{Workspace: *w, Locks: m.Arb.List(id)}
	m.mu.Lock()
	for _, c := range m.cursors {
		if c.Workspace == id && !c.Status.Terminal() {
			v.Cursors++
		}
	}
	v.Usage = m.usage[id]
	r := m.rt[id]
	m.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		if r.display != nil {
			v.Display = r.display.Name()
		}
		v.Viewers = len(r.viewers)
		v.Seats = len(r.seats)
		v.Processes = len(r.procs)
		eng := r.engine
		r.mu.Unlock()
		if eng != nil {
			v.Stream = eng.Stats()
		}
		r.brMu.Lock()
		v.Browser = r.browser != nil
		r.brMu.Unlock()
	}
	return v, nil
}

func (m *Manager) ListWorkspaces() []*WorkspaceView {
	m.mu.Lock()
	ids := make([]string, 0, len(m.ws))
	for id := range m.ws {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	out := []*WorkspaceView{}
	for _, id := range ids {
		if v, err := m.ViewWorkspace(id); err == nil {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (m *Manager) Start(ctx context.Context, id string) error {
	_, err := m.ensureRuntime(ctx, id, true)
	return err
}

func (m *Manager) Checkpoint(ctx context.Context, id, reason string, snapshot bool) (*Checkpoint, error) {
	l := m.wsLock(id)
	l.Lock()
	defer l.Unlock()
	return m.checkpointLocked(ctx, id, reason, snapshot)
}

func (m *Manager) checkpointLocked(ctx context.Context, id, reason string, snapshot bool) (*Checkpoint, error) {
	w, err := m.GetWorkspace(id)
	if err != nil {
		return nil, err
	}
	cp := &Checkpoint{ID: NewID("ckpt"), Time: time.Now().UTC(), Reason: reason, Layout: w.Config.Layout, Width: w.Config.Width, Height: w.Config.Height, Apps: []AppSpec{}, Tabs: []TabRecord{}}
	r := m.runtime(id)
	if r != nil {
		r.brMu.Lock()
		b := r.browser
		r.brMu.Unlock()
		if b != nil {
			cp.Tabs = r.tabRecordsFrom(b)
		} else {
			r.mu.Lock()
			cp.Tabs = append(cp.Tabs, r.lastTabs...)
			r.mu.Unlock()
		}
		r.mu.Lock()
		for _, p := range r.procs {
			if p.App.Restore {
				cp.Apps = append(cp.Apps, p.App)
			}
		}
		if r.display != nil {
			if ws, err := r.display.Windows(); err == nil {
				cp.Windows = len(ws)
			}
		}
		r.mu.Unlock()
		if snapshot {
			dst := filepath.Join(m.cfg.Home, "checkpoints", id, cp.ID)
			if err := m.snapshotProfile(r, w.Config.Profile, dst); err != nil {
				return nil, err
			}
			cp.Snapshot = dst
		}
	} else if w.Checkpoint != nil {
		cp.Tabs = w.Checkpoint.Tabs
		cp.Apps = w.Checkpoint.Apps
	}
	m.mu.Lock()
	for _, c := range m.cursors {
		if c.Workspace == id && !c.Status.Terminal() {
			cp.Cursors = append(cp.Cursors, c.ID)
		}
	}
	if ww := m.ws[id]; ww != nil {
		ww.Checkpoint = cp
		m.saveWS(ww)
	}
	m.mu.Unlock()
	os.MkdirAll(filepath.Join(m.cfg.Home, "checkpoints", id), 0700)
	b, _ := json.MarshalIndent(cp, "", "  ")
	os.WriteFile(filepath.Join(m.cfg.Home, "checkpoints", id, cp.ID+".json"), b, 0600)
	m.Bus.Publish("workspace.checkpoint", id, "", map[string]any{"checkpoint": cp.ID, "tabs": len(cp.Tabs), "apps": len(cp.Apps), "snapshot": cp.Snapshot != "", "reason": reason})
	return cp, nil
}

func (m *Manager) snapshotProfile(r *Runtime, profile, dst string) error {
	src := m.profileDir(profile)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	var pids []int
	if r != nil {
		r.brMu.Lock()
		if r.browser != nil {
			pids = Descendants([]int{r.browser.Pid()})
		}
		r.brMu.Unlock()
	}
	if len(pids) > 0 {
		SignalTree(pids, syscall.SIGSTOP)
		defer SignalTree(pids, syscall.SIGCONT)
	}
	os.MkdirAll(filepath.Dir(dst), 0700)
	out, err := exec.Command("cp", "-a", "--reflink=auto", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("snapshot: %v %s", err, out)
	}
	for _, f := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		os.Remove(filepath.Join(dst, f))
	}
	return nil
}

func (m *Manager) Suspend(ctx context.Context, id, reason string) error {
	l := m.wsLock(id)
	l.Lock()
	defer l.Unlock()
	return m.suspendLocked(ctx, id, reason)
}

func (m *Manager) suspendLocked(ctx context.Context, id, reason string) error {
	r := m.runtime(id)
	if r == nil {
		w, err := m.GetWorkspace(id)
		if err != nil {
			return err
		}
		if w.State == StateCreated {
			return nil
		}
		m.setState(id, StateSuspended, reason)
		return nil
	}
	if r.frozen {
		r.thaw()
	}
	if _, err := m.checkpointLocked(ctx, id, "suspend:"+reason, false); err != nil {
		return err
	}
	r.shutdown(ctx)
	m.mu.Lock()
	delete(m.rt, id)
	delete(m.usage, id)
	m.mu.Unlock()
	if m.cg != nil {
		m.cg.Remove(id)
	}
	m.setState(id, StateSuspended, reason)
	return nil
}

func (m *Manager) Resume(ctx context.Context, id string) error {
	l := m.wsLock(id)
	l.Lock()
	defer l.Unlock()
	w, err := m.GetWorkspace(id)
	if err != nil {
		return err
	}
	r, err := m.ensureRuntimeLocked(ctx, id, true)
	if err != nil {
		return err
	}
	if w.Checkpoint != nil {
		for _, a := range w.Checkpoint.Apps {
			r.launch(a)
		}
		if len(w.Checkpoint.Tabs) > 0 {
			go func() {
				c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				r.Browser(c)
			}()
		}
	}
	m.Bus.Publish("workspace.resumed", id, "", nil)
	return nil
}

func (m *Manager) Freeze(ctx context.Context, id string) error {
	l := m.wsLock(id)
	l.Lock()
	defer l.Unlock()
	r := m.runtime(id)
	if r == nil {
		return ErrState
	}
	if err := r.freeze(); err != nil {
		return err
	}
	m.setState(id, StateFrozen, "freeze")
	return nil
}

func (m *Manager) Thaw(ctx context.Context, id string) error {
	_, err := m.ensureRuntime(ctx, id, false)
	return err
}

func (m *Manager) Fork(ctx context.Context, id, name string) (*Workspace, error) {
	src, err := m.GetWorkspace(id)
	if err != nil {
		return nil, err
	}
	nid := NewID("ws")
	cfg := src.Config
	cfg.Profile = nid
	r := m.runtime(id)
	if err := m.snapshotProfile(r, src.Config.Profile, m.profileDir(nid)); err != nil {
		return nil, err
	}
	m.chownApps(m.profileDir(nid))
	srcHome := filepath.Join(m.wsDir(id), "home")
	if _, err := os.Stat(srcHome); err == nil {
		os.MkdirAll(m.wsDir(nid), 0711)
		exec.Command("cp", "-a", "--reflink=auto", srcHome, filepath.Join(m.wsDir(nid), "home")).Run()
	}
	if name == "" {
		name = src.Name + "-fork"
	}
	now := time.Now().UTC()
	w := &Workspace{ID: nid, Name: name, State: StateCreated, Config: cfg, Owner: src.Owner, ForkOf: id, Created: now, Updated: now, LastActive: now}
	var tabs []TabRecord
	if r != nil {
		r.brMu.Lock()
		b := r.browser
		r.brMu.Unlock()
		if b != nil {
			for _, t := range r.tabRecordsFrom(b) {
				tabs = append(tabs, TabRecord{URL: t.URL, Title: t.Title})
			}
		}
	} else if src.Checkpoint != nil {
		for _, t := range src.Checkpoint.Tabs {
			tabs = append(tabs, TabRecord{URL: t.URL, Title: t.Title})
		}
	}
	if len(tabs) > 0 {
		w.Checkpoint = &Checkpoint{ID: NewID("ckpt"), Time: now, Reason: "fork", Tabs: tabs}
	}
	m.mu.Lock()
	m.ws[nid] = w
	m.saveWS(w)
	m.mu.Unlock()
	m.Bus.Publish("workspace.forked", nid, "", map[string]any{"from": id})
	return w, nil
}

func (m *Manager) Destroy(ctx context.Context, id string, purge bool) error {
	l := m.wsLock(id)
	l.Lock()
	defer l.Unlock()
	w, err := m.GetWorkspace(id)
	if err != nil {
		return err
	}
	if r := m.runtime(id); r != nil {
		r.shutdown(ctx)
		m.mu.Lock()
		delete(m.rt, id)
		m.mu.Unlock()
		if m.cg != nil {
			m.cg.Remove(id)
		}
	}
	m.mu.Lock()
	var cancel []string
	for _, c := range m.cursors {
		if c.Workspace == id && !c.Status.Terminal() {
			cancel = append(cancel, c.ID)
		}
	}
	m.mu.Unlock()
	for _, c := range cancel {
		m.CancelCursor(c, "workspace destroyed", true)
	}
	m.Arb.Clear(id)
	if purge {
		shared := false
		m.mu.Lock()
		for oid, o := range m.ws {
			if oid != id && o.Config.Profile == w.Config.Profile && o.State != StateDestroyed {
				shared = true
			}
		}
		m.mu.Unlock()
		if !shared {
			os.RemoveAll(m.profileDir(w.Config.Profile))
		}
		os.RemoveAll(m.wsDir(id))
		os.RemoveAll(filepath.Join(m.cfg.Home, "checkpoints", id))
		m.Bus.CloseLog(id)
		os.Remove(filepath.Join(m.cfg.Home, "events", id+".jsonl"))
		m.mu.Lock()
		delete(m.ws, id)
		delete(m.usage, id)
		m.mu.Unlock()
		m.store.Delete("workspaces", id)
	} else {
		m.setState(id, StateDestroyed, "destroy")
		m.mu.Lock()
		delete(m.usage, id)
		m.mu.Unlock()
	}
	m.Bus.Publish("workspace.destroyed", id, "", map[string]any{"purged": purge})
	return nil
}

func (m *Manager) Resize(ctx context.Context, id string, w, h int) error {
	if w < 320 || h < 240 || w > 1920 || h > 1200 {
		return fmt.Errorf("size must be between 320x240 and 1920x1200")
	}
	r, err := m.ensureRuntime(ctx, id, true)
	if err != nil {
		return err
	}
	r.mu.Lock()
	d, wm := r.display, r.wm
	r.mu.Unlock()
	if d == nil {
		return ErrState
	}
	if err := d.SetSize(w, h); err != nil {
		return err
	}
	if wm != nil {
		wm.ScreenChanged()
	}
	m.mu.Lock()
	if ws := m.ws[id]; ws != nil {
		ws.Config.Width, ws.Config.Height = w, h
		m.saveWS(ws)
	}
	m.mu.Unlock()
	r.mu.Lock()
	eng := r.engine
	r.mu.Unlock()
	if eng != nil {
		eng.RequestKeyframe()
	}
	m.Bus.Publish("desktop.resized", id, "", map[string]any{"width": w, "height": h})
	return nil
}

func (m *Manager) SetLayout(ctx context.Context, id, layout string) error {
	r, err := m.ensureRuntime(ctx, id, true)
	if err != nil {
		return err
	}
	if r.wm == nil {
		return ErrState
	}
	r.wm.SetLayout(x11Layout(layout))
	m.mu.Lock()
	if ws := m.ws[id]; ws != nil {
		ws.Config.Layout = layout
		m.saveWS(ws)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) LaunchApp(ctx context.Context, id string, spec AppSpec) (*Proc, error) {
	r, err := m.ensureRuntime(ctx, id, true)
	if err != nil {
		return nil, err
	}
	r.touch()
	return r.launch(spec)
}

func (r *Runtime) launch(spec AppSpec) (*Proc, error) {
	if spec.ID == "" {
		spec.ID = NewID("app")
	}
	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Env = r.appEnv()
	cmd.Dir = filepath.Join(r.m.wsDir(r.id), "home")
	attr := &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if r.m.uid != 0 {
		attr.Credential = &syscall.Credential{Uid: uint32(r.m.uid), Gid: uint32(r.m.gid)}
	}
	cmd.SysProcAttr = attr
	logf, _ := os.OpenFile(filepath.Join(r.m.wsDir(r.id), "run", spec.ID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		return nil, err
	}
	if r.m.cg != nil {
		r.m.cg.Add(r.id, cmd.Process.Pid)
	}
	p := &Proc{Pid: cmd.Process.Pid, App: spec, Started: time.Now(), cmd: cmd, done: make(chan struct{})}
	r.mu.Lock()
	r.procs[p.Pid] = p
	r.mu.Unlock()
	r.m.Bus.Publish("process.started", r.id, spec.Owner, map[string]any{"pid": p.Pid, "command": spec.Command, "app": spec.ID})
	go func() {
		err := cmd.Wait()
		if logf != nil {
			logf.Close()
		}
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		p.Exit = &code
		close(p.done)
		r.mu.Lock()
		delete(r.procs, p.Pid)
		r.mu.Unlock()
		r.m.Bus.Publish("process.exited", r.id, spec.Owner, map[string]any{"pid": p.Pid, "command": spec.Command, "code": code, "app": spec.ID})
	}()
	return p, nil
}

func (m *Manager) Processes(id string) []Proc {
	r := m.runtime(id)
	out := []Proc{}
	if r == nil {
		return out
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.procs {
		out = append(out, *p)
	}
	return out
}

func (m *Manager) KillProcess(id string, pid int) error {
	r := m.runtime(id)
	if r == nil {
		return ErrNotFound
	}
	r.mu.Lock()
	p := r.procs[pid]
	r.mu.Unlock()
	if p == nil {
		return ErrNotFound
	}
	return syscall.Kill(-pid, syscall.SIGTERM)
}
