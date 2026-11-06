package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/olimpiacloud/agora/internal/browser"
	"github.com/olimpiacloud/agora/internal/stream"
	"github.com/olimpiacloud/agora/internal/x11"
)

type Proc struct {
	Pid     int       `json:"pid"`
	App     AppSpec   `json:"app"`
	Started time.Time `json:"started"`
	cmd     *exec.Cmd
	done    chan struct{}
	Exit    *int `json:"exit,omitempty"`
}

type seatLease struct {
	seat     *x11.Seat
	lastUsed time.Time
}

type Runtime struct {
	m         *Manager
	id        string
	mu        sync.Mutex
	display   *x11.Display
	wm        *x11.WM
	clip      *x11.Clipboard
	brMu      sync.Mutex
	browser   *browser.Browser
	brCrashes int
	engine    *stream.Engine
	core      *x11.Seat
	seats     map[string]*seatLease
	procs     map[int]*Proc
	frozen    bool
	viewers   map[string]*Viewer
	activity  time.Time
	started   time.Time
	tabOwner  map[string]string
	lastTabs  []TabRecord
	idle      bool
	closing   bool
	restored  bool
	lastObs   map[string]string
	offsets   map[string][3]int
}

func (m *Manager) runtime(id string) *Runtime {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rt[id]
}

func (r *Runtime) touch() {
	r.mu.Lock()
	r.activity = time.Now()
	wasIdle := r.idle
	r.idle = false
	r.mu.Unlock()
	if wasIdle {
		r.m.setState(r.id, StateActive, "activity")
	}
}

func (m *Manager) setState(id string, st WSState, reason string) {
	m.mu.Lock()
	w := m.ws[id]
	if w == nil || w.State == st {
		m.mu.Unlock()
		return
	}
	prev := w.State
	w.State = st
	if st == StateActive {
		w.LastActive = time.Now().UTC()
	}
	m.saveWS(w)
	m.mu.Unlock()
	m.Bus.Publish("workspace.state", id, "", map[string]any{"from": string(prev), "to": string(st), "reason": reason})
}

func (m *Manager) ensureRuntime(ctx context.Context, id string, display bool) (*Runtime, error) {
	l := m.wsLock(id)
	l.Lock()
	defer l.Unlock()
	return m.ensureRuntimeLocked(ctx, id, display)
}

func (m *Manager) ensureRuntimeLocked(ctx context.Context, id string, display bool) (*Runtime, error) {
	m.mu.Lock()
	w := m.ws[id]
	if w == nil || w.State == StateDestroyed {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	r := m.rt[id]
	cfg := w.Config
	m.mu.Unlock()
	if r == nil {
		r = &Runtime{m: m, id: id, seats: map[string]*seatLease{}, procs: map[int]*Proc{}, viewers: map[string]*Viewer{}, activity: time.Now(), started: time.Now(), tabOwner: map[string]string{}, offsets: map[string][3]int{}}
		home := m.wsDir(id)
		os.MkdirAll(filepath.Join(home, "home", "Downloads"), 0700)
		os.MkdirAll(filepath.Join(home, "run"), 0711)
		os.MkdirAll(filepath.Join(home, "home", ".xdg-run"), 0700)
		for _, p := range []string{home, filepath.Join(home, "home"), filepath.Join(home, "home", "Downloads"), filepath.Join(home, "home", ".xdg-run")} {
			m.chownApps(p)
		}
		if m.cg != nil {
			m.cg.Ensure(id, cfg.Budget)
		}
		m.mu.Lock()
		m.rt[id] = r
		m.mu.Unlock()
		if w.Checkpoint != nil {
			r.lastTabs = w.Checkpoint.Tabs
		}
	}
	if r.frozen {
		if err := r.thaw(); err != nil {
			return nil, err
		}
	}
	if display && !cfg.Headless && r.display == nil {
		if err := r.startDisplay(ctx, cfg); err != nil {
			return nil, err
		}
	}
	if !r.restored && (r.display != nil || cfg.Headless) {
		r.restored = true
		m.mu.Lock()
		cp := w.Checkpoint
		m.mu.Unlock()
		if cp != nil {
			for _, a := range cp.Apps {
				r.launch(a)
			}
			if len(cp.Tabs) > 0 {
				go func() {
					c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					r.Browser(c)
				}()
			}
		}
	}
	m.setState(id, StateActive, "runtime")
	return r, nil
}

func (r *Runtime) startDisplay(ctx context.Context, cfg WorkspaceConfig) error {
	m := r.m
	w, h := cfg.Width, cfg.Height
	if w == 0 {
		w, h = m.cfg.Width, m.cfg.Height
	}
	cgfd, release := r.cgroupFD()
	d, err := x11.StartDisplay(ctx, x11.DisplayOptions{Width: w, Height: h, RunDir: filepath.Join(m.wsDir(r.id), "run"), AuthUid: m.uid, AuthGid: m.gid, CgroupFD: cgfd})
	release()
	if err != nil {
		return fmt.Errorf("display: %w", err)
	}
	layout := x11.Layout(cfg.Layout)
	if layout == "" {
		layout = x11.LayoutTile
	}
	wm, err := d.StartWM(layout)
	if err != nil {
		d.Close()
		return err
	}
	wm.OnChange = func() { m.Bus.Publish("desktop.windows", r.id, "", nil) }
	core, err := d.CoreSeat()
	if err != nil {
		wm.Close()
		d.Close()
		return err
	}
	clip, _ := d.NewClipboard()
	r.mu.Lock()
	r.display, r.wm, r.core, r.clip = d, wm, core, clip
	r.engine = stream.NewEngine(d)
	r.engine.OnState = func(on bool) {
		m.Bus.Publish("stream.state", r.id, "", map[string]any{"encoding": on})
	}
	r.mu.Unlock()
	go func() {
		<-d.Exited()
		r.mu.Lock()
		closing := r.closing
		cur := r.display
		r.mu.Unlock()
		if !closing && cur == d {
			m.Bus.Publish("desktop.crashed", r.id, "", map[string]any{"display": d.Name()})
			r.mu.Lock()
			r.display, r.wm, r.core, r.engine = nil, nil, nil, nil
			r.mu.Unlock()
		}
	}()
	m.Bus.Publish("desktop.started", r.id, "", map[string]any{"display": d.Name(), "width": w, "height": h})
	return nil
}

func (r *Runtime) appEnv() []string {
	home := filepath.Join(r.m.wsDir(r.id), "home")
	env := []string{
		"HOME=" + home,
		"USER=" + r.m.cfg.AppsUser,
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"DBUS_SESSION_BUS_ADDRESS=disabled:",
		"XDG_RUNTIME_DIR=" + filepath.Join(home, ".xdg-run"),
		"AGORA_WORKSPACE=" + r.id,
	}
	if r.display != nil {
		env = append(env, r.display.Env()...)
	}
	return env
}

func (r *Runtime) Browser(ctx context.Context) (*browser.Browser, error) {
	r.brMu.Lock()
	defer r.brMu.Unlock()
	if r.browser != nil {
		select {
		case <-r.browser.Exited():
			r.browser = nil
		default:
			return r.browser, nil
		}
	}
	m := r.m
	m.mu.Lock()
	w := m.ws[r.id]
	cfg := w.Config
	m.mu.Unlock()
	if !cfg.Headless && r.display == nil {
		return nil, errors.New("workspace display not running")
	}
	profile := cfg.Profile
	if profile == "" {
		profile = r.id
	}
	if other := m.profileHolder(profile, r.id); other != "" {
		return nil, fmt.Errorf("profile %q is open in workspace %s; fork the workspace or suspend it first", profile, other)
	}
	pdir := m.profileDir(profile)
	dl := filepath.Join(m.wsDir(r.id), "home", "Downloads")
	sw, sh := cfg.Width, cfg.Height
	if sw == 0 {
		sw, sh = m.cfg.Width, m.cfg.Height
	}
	var start []string
	restore := r.lastTabs
	if len(restore) == 0 && len(cfg.AutostartURLs) > 0 {
		for _, u := range cfg.AutostartURLs {
			restore = append(restore, TabRecord{URL: u})
		}
	}
	cgfd, release := r.cgroupFD()
	defer release()
	b, err := browser.Launch(ctx, browser.LaunchOptions{CgroupFD: cgfd, Binary: m.cfg.ChromeBinary, UserDataDir: pdir, Env: r.appEnv(), Uid: uint32(m.uid), Gid: uint32(m.gid), Headless: cfg.Headless, Width: sw, Height: sh, ExtraArgs: cfg.BrowserArgs, DownloadDir: dl, LogPath: filepath.Join(m.wsDir(r.id), "run", "browser.log"), StartURLs: start})
	if err != nil {
		return nil, err
	}
	b.OnEvent = func(kind string, data map[string]any) {
		cur := ""
		if t, ok := data["target"].(string); ok {
			r.mu.Lock()
			cur = r.tabOwner[t]
			r.mu.Unlock()
		}
		if kind == "browser.challenge.detected" && cur != "" {
			m.setCursorStatus(cur, CursorNeedHuman, fmt.Sprintf("%v challenge on %v", data["kind"], data["url"]), false)
		}
		if kind == "browser.tab.created" {
			if op, _ := data["opener"].(string); op != "" {
				r.mu.Lock()
				if o := r.tabOwner[op]; o != "" {
					r.tabOwner[data["target"].(string)] = o
					cur = o
				}
				r.mu.Unlock()
			}
		}
		m.Bus.Publish(kind, r.id, cur, data)
	}
	r.browser = b
	m.Bus.Publish("browser.started", r.id, "", map[string]any{"pid": b.Pid(), "profile": profile})
	go r.watchBrowser(b)
	if len(restore) > 0 {
		go r.restoreTabs(b, restore)
	}
	return b, nil
}

func (r *Runtime) restoreTabs(b *browser.Browser, tabs []TabRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	initial := b.Targets()
	var wg sync.WaitGroup
	var mu sync.Mutex
	restored := 0
	for _, t := range tabs {
		if t.URL == "" || t.URL == "about:blank" || strings.HasPrefix(t.URL, "chrome://newtab") {
			continue
		}
		wg.Add(1)
		go func(t TabRecord) {
			defer wg.Done()
			tab, err := b.NewTab(ctx, browser.NewTabOptions{URL: t.URL, NewWindow: true})
			if err != nil {
				return
			}
			mu.Lock()
			restored++
			mu.Unlock()
			if t.Owner != "" {
				r.mu.Lock()
				r.tabOwner[tab.TargetID] = t.Owner
				r.mu.Unlock()
				tab.Owner = t.Owner
				r.m.mu.Lock()
				if c := r.m.cursors[t.Owner]; c != nil && !c.Status.Terminal() {
					c.Surface = "tab:" + tab.TargetID
					r.m.saveCursor(c)
				}
				r.m.mu.Unlock()
			}
			r.m.Bus.Publish("browser.tab.restored", r.id, t.Owner, map[string]any{"target": tab.TargetID, "url": t.URL, "previous": t.Target})
		}(t)
	}
	wg.Wait()
	if restored > 0 {
		for _, t := range initial {
			if t.URL == "about:blank" {
				b.CDP().Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": t.TargetID}, nil)
			}
		}
	}
	r.mu.Lock()
	r.lastTabs = nil
	r.mu.Unlock()
}

func (r *Runtime) watchBrowser(b *browser.Browser) {
	<-b.Exited()
	r.brMu.Lock()
	current := r.browser == b
	if current {
		r.browser = nil
	}
	r.brMu.Unlock()
	if !current {
		return
	}
	r.brCrashes++
	tabs := r.tabRecordsFrom(b)
	r.mu.Lock()
	r.lastTabs = tabs
	r.mu.Unlock()
	r.m.Bus.Publish("browser.crashed", r.id, "", map[string]any{"tabs": len(tabs), "crashes": r.brCrashes, "note": "next browser call relaunches with the persistent profile and restores tabs"})
}

func (r *Runtime) tabRecordsFrom(b *browser.Browser) []TabRecord {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	b.Refresh(ctx)
	cancel()
	out := []TabRecord{}
	for _, t := range b.Targets() {
		r.mu.Lock()
		owner := r.tabOwner[t.TargetID]
		r.mu.Unlock()
		out = append(out, TabRecord{Target: t.TargetID, URL: t.URL, Title: t.Title, Owner: owner, Context: t.BrowserContextID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Owner < out[j].Owner })
	return out
}

func (m *Manager) profileHolder(profile, except string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.rt {
		if id == except {
			continue
		}
		w := m.ws[id]
		p := w.Config.Profile
		if p == "" {
			p = id
		}
		if p == profile {
			r.brMu.Lock()
			running := r.browser != nil
			r.brMu.Unlock()
			if running {
				return id
			}
		}
	}
	return ""
}

func (r *Runtime) cgroupFD() (int, func()) {
	if r.m.cg == nil {
		return -1, func() {}
	}
	return r.m.cg.FD(r.id)
}

func (r *Runtime) allPids() []int {
	if r.m.cg != nil {
		if p := r.m.cg.Pids(r.id); len(p) > 0 {
			return p
		}
	}
	roots := []int{}
	r.mu.Lock()
	if r.display != nil {
		roots = append(roots, r.display.Pid())
	}
	for pid := range r.procs {
		roots = append(roots, pid)
	}
	r.mu.Unlock()
	r.brMu.Lock()
	if r.browser != nil {
		roots = append(roots, r.browser.Pid())
	}
	r.brMu.Unlock()
	return Descendants(roots)
}

func (r *Runtime) freeze() error {
	r.mu.Lock()
	if r.frozen {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	r.closeViewers("workspace frozen")
	r.releaseSeats(0)
	if r.m.cg != nil {
		if err := r.m.cg.Freeze(r.id, true); err == nil {
			r.mu.Lock()
			r.frozen = true
			r.mu.Unlock()
			return nil
		}
	}
	SignalTree(r.allPids(), syscall.SIGSTOP)
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
	return nil
}

func (r *Runtime) thaw() error {
	r.mu.Lock()
	if !r.frozen {
		r.mu.Unlock()
		return nil
	}
	r.frozen = false
	r.activity = time.Now()
	r.mu.Unlock()
	if r.m.cg != nil {
		if err := r.m.cg.Freeze(r.id, false); err == nil {
			return nil
		}
	}
	SignalTree(r.allPids(), syscall.SIGCONT)
	return nil
}

func (r *Runtime) releaseSeats(idle time.Duration) int {
	r.mu.Lock()
	var toClose []*x11.Seat
	for cid, s := range r.seats {
		if idle == 0 || time.Since(s.lastUsed) > idle {
			toClose = append(toClose, s.seat)
			delete(r.seats, cid)
		}
	}
	r.mu.Unlock()
	for _, s := range toClose {
		s.Close()
	}
	return len(toClose)
}

func (r *Runtime) seatFor(cursorID string) (*x11.Seat, error) {
	r.mu.Lock()
	if s := r.seats[cursorID]; s != nil {
		s.lastUsed = time.Now()
		r.mu.Unlock()
		return s.seat, nil
	}
	d := r.display
	n := len(r.seats)
	r.mu.Unlock()
	if d == nil {
		return nil, errors.New("display not running")
	}
	if n >= r.m.cfg.MaxSeats {
		if r.releaseSeats(10*time.Second) == 0 {
			return nil, errors.New("all input seats busy; retry or use semantic (tab) input")
		}
	}
	name := "c-" + strings.TrimPrefix(cursorID, "cur_")
	s, err := d.NewSeat(name)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.seats[cursorID] = &seatLease{seat: s, lastUsed: time.Now()}
	r.mu.Unlock()
	return s, nil
}

func (r *Runtime) shutdown(ctx context.Context) {
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	r.closeViewers("workspace stopping")
	r.brMu.Lock()
	b := r.browser
	r.browser = nil
	r.brMu.Unlock()
	if b != nil {
		b.Close(ctx)
	}
	r.mu.Lock()
	procs := make([]*Proc, 0, len(r.procs))
	for _, p := range r.procs {
		procs = append(procs, p)
	}
	r.mu.Unlock()
	for _, p := range procs {
		syscall.Kill(-p.Pid, syscall.SIGTERM)
	}
	for _, p := range procs {
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
	}
	r.releaseSeats(0)
	r.mu.Lock()
	eng, clip, core, wm, d := r.engine, r.clip, r.core, r.wm, r.display
	r.engine, r.clip, r.core, r.wm, r.display = nil, nil, nil, nil, nil
	r.mu.Unlock()
	if eng != nil {
		eng.Close()
	}
	if clip != nil {
		clip.Close()
	}
	if core != nil {
		core.Close()
	}
	if wm != nil {
		wm.Close()
	}
	if d != nil {
		d.Close()
	}
}
