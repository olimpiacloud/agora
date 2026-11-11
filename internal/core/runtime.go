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
	"github.com/olimpiacloud/agora/internal/safefs"
	"github.com/olimpiacloud/agora/internal/sandbox"
	"github.com/olimpiacloud/agora/internal/stream"
	"github.com/olimpiacloud/agora/internal/x11"
	"golang.org/x/sys/unix"
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
	m          *Manager
	id         string
	mu         sync.Mutex
	display    *x11.Display
	wm         *x11.WM
	clip       *x11.Clipboard
	brMu       sync.Mutex
	browser    *browser.Browser
	brCrashes  int
	noTabs     time.Time
	human      time.Time
	reaping    bool
	engine     *stream.Engine
	core       *x11.Seat
	seats      map[string]*seatLease
	procs      map[int]*Proc
	frozen     bool
	viewers    map[string]*Viewer
	activity   time.Time
	started    time.Time
	tabOwner   map[string]string
	lastTabs   []TabRecord
	idle       bool
	closing    bool
	suspending bool
	restored   bool
	lastObs    map[string]string
	offsets    map[string][3]int
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
	if w == nil || w.State == st || w.State == StateDestroyed {
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
		if err := m.prepareWSDir(id); err != nil {
			return nil, fmt.Errorf("workspace dir: %w", err)
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
	if err := r.thaw(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	d := r.display
	r.mu.Unlock()
	if display && !cfg.Headless && d == nil {
		if err := r.startDisplay(ctx, cfg); err != nil {
			return nil, err
		}
		r.mu.Lock()
		d = r.display
		r.mu.Unlock()
	}
	if !r.restored && (d != nil || cfg.Headless) {
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

func (m *Manager) prepareWSDir(id string) error {
	dir := m.wsDir(id)
	if err := os.MkdirAll(dir, 0711); err != nil {
		return err
	}
	if m.uid != 0 {
		d, err := safefs.OpenBeneath(dir, ".", syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		err = d.Chown(0, 0)
		if err == nil {
			err = d.Chmod(0711)
		}
		d.Close()
		if err != nil {
			return err
		}
		for _, n := range []string{"home", "run"} {
			p := filepath.Join(dir, n)
			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if !st.IsDir() || (n == "run" && st.Sys().(*syscall.Stat_t).Uid != 0) {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
			}
		}
	}
	if err := safefs.MkdirBeneath(dir, "run", 0711); err != nil {
		return err
	}
	if err := safefs.MkdirBeneath(dir, "home", 0700); err != nil {
		return err
	}
	for _, n := range []string{"Downloads", ".xdg-run"} {
		if err := mkdirLeaf(dir, "home", n); err != nil {
			return err
		}
	}
	if m.uid != 0 {
		for _, rel := range []string{"home", "home/Downloads", "home/.xdg-run"} {
			if err := safefs.ChownBeneath(dir, rel, m.uid, m.gid); err != nil {
				return err
			}
		}
	}
	return nil
}

func mkdirLeaf(base, parent, name string) error {
	var err error
	for range 3 {
		var d *os.File
		d, err = safefs.OpenBeneath(base, parent, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		var st unix.Stat_t
		if unix.Fstatat(int(d.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT != unix.S_IFDIR {
			if e := unix.Unlinkat(int(d.Fd()), name, 0); e != nil && e != unix.ENOENT {
				d.Close()
				return &os.PathError{Op: "unlink", Path: filepath.Join(base, parent, name), Err: e}
			}
		}
		d.Close()
		if err = safefs.MkdirBeneath(base, filepath.Join(parent, name), 0700); err == nil {
			return nil
		}
	}
	return err
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
			eng, clip, core, wm := r.engine, r.clip, r.core, r.wm
			seats := r.seats
			r.display, r.wm, r.core, r.engine, r.clip = nil, nil, nil, nil, nil
			r.seats = map[string]*seatLease{}
			r.mu.Unlock()
			r.closeViewers("display crashed")
			for _, s := range seats {
				s.seat.Close()
			}
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
			d.Close()
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
	r.mu.Lock()
	d := r.display
	r.mu.Unlock()
	if d != nil {
		env = append(env, d.Env()...)
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
	r.mu.Lock()
	closing := r.closing
	d := r.display
	r.mu.Unlock()
	if closing {
		return nil, fmt.Errorf("%w: workspace is stopping", ErrState)
	}
	m := r.m
	m.mu.Lock()
	w := m.ws[r.id]
	cfg := w.Config
	m.mu.Unlock()
	if !cfg.Headless && d == nil {
		return nil, errors.New("workspace display not running")
	}
	profile := cfg.Profile
	if profile == "" {
		profile = r.id
	}
	lease, other := m.reserveProfile(profile, r.id)
	if other != "" {
		return nil, fmt.Errorf("profile %q is open in workspace %s; fork the workspace or suspend it first", profile, other)
	}
	launched := false
	defer func() {
		if !launched {
			m.releaseProfile(profile, lease)
		}
	}()
	pdir := m.profileDir(profile)
	dl := filepath.Join(m.wsDir(r.id), "home", "Downloads")
	sw, sh := cfg.Width, cfg.Height
	if sw == 0 {
		sw, sh = m.cfg.Width, m.cfg.Height
	}
	restore := r.lastTabs
	if len(restore) == 0 && len(cfg.AutostartURLs) > 0 {
		for _, u := range cfg.AutostartURLs {
			if browser.AllowedURL(u) != nil {
				continue
			}
			restore = append(restore, TabRecord{URL: u})
		}
	}
	cgfd, release := r.cgroupFD()
	defer release()
	var err error
	var wrap []string
	if cfg.Sandbox {
		if wrap, err = r.sandboxWrap(pdir); err != nil {
			return nil, err
		}
	}
	onEvent := func(kind string, data map[string]any) {
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
	b, err := browser.Launch(ctx, browser.LaunchOptions{Wrap: wrap, CgroupFD: cgfd, Binary: m.cfg.ChromeBinary, UserDataDir: pdir, Env: r.appEnv(), Uid: uint32(m.uid), Gid: uint32(m.gid), Headless: cfg.Headless, Width: sw, Height: sh, ExtraArgs: cfg.BrowserArgs, DownloadDir: dl, LogPath: filepath.Join(m.wsDir(r.id), "run", "browser.log"), OnEvent: onEvent})
	if err != nil {
		return nil, err
	}
	launched = true
	m.mu.Lock()
	lease.b = b
	m.mu.Unlock()
	r.browser = b
	m.Bus.Publish("browser.started", r.id, "", map[string]any{"pid": b.Pid(), "profile": profile})
	go r.watchBrowser(b, profile, lease)
	if len(restore) > 0 {
		go r.restoreTabs(b, restore)
	}
	return b, nil
}

func (r *Runtime) sandboxWrap(extra ...string) ([]string, error) {
	m := r.m
	if m.uid == 0 {
		return nil, errors.New("sandbox needs the daemon to run as root with an apps user")
	}
	self := m.cfg.AgoraBinary
	if self == "" {
		self, _ = os.Executable()
	}
	binds := append([]string{m.wsDir(r.id)}, extra...)
	r.mu.Lock()
	if r.display != nil {
		binds = append(binds, r.display.Socket())
	}
	r.mu.Unlock()
	return sandbox.Wrap(self, sandbox.Spec{Uid: m.uid, Gid: m.gid, Binds: binds, Hide: []string{m.cfg.Home}}), nil
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
				tab.SetOwner(t.Owner)
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

func (r *Runtime) watchBrowser(b *browser.Browser, profile string, lease *profileLease) {
	<-b.Exited()
	r.m.releaseProfile(profile, lease)
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
	return r.cachedTabRecords(b)
}

var liveTabRecords = (*Runtime).tabRecordsFrom

func (r *Runtime) currentTabs() []TabRecord {
	r.mu.Lock()
	frozen := r.frozen
	last := append([]TabRecord{}, r.lastTabs...)
	r.mu.Unlock()
	r.brMu.Lock()
	b := r.browser
	r.brMu.Unlock()
	switch {
	case b == nil:
		return last
	case frozen:
		return r.cachedTabRecords(b)
	default:
		return liveTabRecords(r, b)
	}
}

func (r *Runtime) cachedTabRecords(b *browser.Browser) []TabRecord {
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

type profileLease struct {
	ws string
	b  *browser.Browser
}

func (m *Manager) reserveProfile(profile, ws string) (*profileLease, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l := m.profiles[profile]; l != nil && l.ws != ws {
		running := l.b == nil
		if l.b != nil {
			select {
			case <-l.b.Exited():
			default:
				running = true
			}
		}
		if running {
			return nil, l.ws
		}
	}
	l := &profileLease{ws: ws}
	m.profiles[profile] = l
	return l, ""
}

func (m *Manager) releaseProfile(profile string, l *profileLease) {
	m.mu.Lock()
	if m.profiles[profile] == l {
		delete(m.profiles, profile)
	}
	m.mu.Unlock()
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
	if cur := r.seats[cursorID]; cur != nil || r.display != d {
		r.mu.Unlock()
		s.Close()
		if cur == nil {
			return nil, errors.New("display restarted; retry")
		}
		return cur.seat, nil
	}
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
