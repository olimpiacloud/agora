package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/olimpiacloud/agora/internal/sandbox"
	"github.com/olimpiacloud/agora/internal/stream"
)

type Config struct {
	Home               string
	AppsUser           string
	XUser              string
	ChromeBinary       string
	Width              int
	Height             int
	MaxSeats           int
	SeatIdle           time.Duration
	IdleAfter          time.Duration
	SuspendAfter       time.Duration
	TabIdle            time.Duration
	CursorRetention    time.Duration
	WorkspaceRetention time.Duration
	AppKillGrace       time.Duration
	MaxAgents          int
	AgentMaxRuntime    time.Duration
	RTC                stream.RTCOptions
	AgoraBinary        string
	ListenURL          string
	WorktreeRoots      []string
	SandboxDefault     bool
}

const DefaultCursorRetention = 7 * 24 * time.Hour

const DefaultWorkspaceRetention = 7 * 24 * time.Hour
const DefaultMaxAgents = 16

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrState     = errors.New("invalid state")
)

type Manager struct {
	cfg       Config
	store     *Store
	Bus       *Bus
	Arb       *Arbiter
	cg        *Cgroups
	rtc       *stream.RTC
	uid, gid  int
	xuid      int
	xgid      int
	sandboxed bool
	mu        sync.Mutex
	ws        map[string]*Workspace
	rt        map[string]*Runtime
	wsLocks   map[string]*sync.Mutex
	cursors   map[string]*Cursor
	tokens    map[string]string
	worktrees map[string]*Worktree
	mail      map[string][]Message
	boards    map[string]*board
	mailSeq   int64
	launches  map[string]*launchProc
	agents    sync.WaitGroup
	cpuPrev   map[string]cpuSample
	usage     map[string]*WorkspaceUsage
	profiles  map[string]*profileLease
	portsHeld map[int]bool
	expiring  map[string]bool
	wtPending map[string]int
	shots     shotStore
	started   time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.Width == 0 {
		cfg.Width = 1280
	}
	if cfg.Height == 0 {
		cfg.Height = 800
	}
	if cfg.MaxSeats == 0 {
		cfg.MaxSeats = 6
	}
	if cfg.SeatIdle == 0 {
		cfg.SeatIdle = 2 * time.Minute
	}
	if cfg.IdleAfter == 0 {
		cfg.IdleAfter = 5 * time.Minute
	}
	if cfg.AppKillGrace == 0 {
		cfg.AppKillGrace = 10 * time.Second
	}
	var roots []string
	for _, root := range cfg.WorktreeRoots {
		root = strings.TrimSpace(root)
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("worktree root %q is not an absolute path", root)
		}
		roots = append(roots, filepath.Clean(root))
	}
	cfg.WorktreeRoots = roots
	for _, d := range []string{"state", "profiles", "workspaces", "events", "run", "checkpoints", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(cfg.Home, d), 0700); err != nil {
			return nil, err
		}
	}
	os.Chmod(cfg.Home, 0711)
	for _, d := range []string{"profiles", "workspaces", "run"} {
		os.Chmod(filepath.Join(cfg.Home, d), 0711)
	}
	st, err := NewStore(filepath.Join(cfg.Home, "state"))
	if err != nil {
		return nil, err
	}
	m := &Manager{cfg: cfg, store: st, Bus: NewBus(filepath.Join(cfg.Home, "events"), 4096), ws: map[string]*Workspace{}, rt: map[string]*Runtime{}, wsLocks: map[string]*sync.Mutex{}, cursors: map[string]*Cursor{}, tokens: map[string]string{}, worktrees: map[string]*Worktree{}, mail: map[string][]Message{}, launches: map[string]*launchProc{}, cpuPrev: map[string]cpuSample{}, usage: map[string]*WorkspaceUsage{}, profiles: map[string]*profileLease{}, portsHeld: map[int]bool{}, started: time.Now()}
	st.onError = func(kind, id string, err error) {
		m.Bus.Publish("store.error", "", "", map[string]any{"kind": kind, "id": id, "error": err.Error()})
	}
	m.Arb = NewArbiter(func(ws string, l Lock, by string) {
		m.Bus.Publish("control.lost", ws, l.Holder, map[string]any{"surface": l.Surface, "by": by})
	})
	if os.Getuid() == 0 && cfg.AppsUser != "" {
		u, err := user.Lookup(cfg.AppsUser)
		if err != nil {
			return nil, fmt.Errorf("apps user %q: %w (create it with: useradd --system --create-home --home-dir /var/lib/agora-apps --shell /usr/sbin/nologin %s)", cfg.AppsUser, err, cfg.AppsUser)
		}
		m.uid, _ = strconv.Atoi(u.Uid)
		m.gid, _ = strconv.Atoi(u.Gid)
		m.xuid, m.xgid, err = lookupXUser(cfg.XUser, m.uid)
		if err != nil {
			log.Printf("Xvfb runs as root: %v", err)
		}
	}
	if cfg.SandboxDefault {
		if err := m.probeSandbox(); err != nil {
			log.Printf("new workspaces are not sandboxed by default: %v", err)
		} else {
			m.sandboxed = true
		}
	}
	m.cg = DetectCgroups()
	rtc, err := stream.NewRTC(cfg.RTC)
	if err != nil {
		return nil, err
	}
	m.rtc = rtc
	wss, _ := LoadAll[Workspace](st, "workspaces")
	for _, w := range wss {
		if w.State == StateActive || w.State == StateIdle || w.State == StateFrozen {
			w.State = StateSuspended
			m.Bus.Publish("workspace.recovered", w.ID, "", map[string]any{"note": "daemon restarted; runtime was lost, persistent data kept"})
		}
		m.ws[w.ID] = w
	}
	cs, _ := LoadAll[Cursor](st, "cursors")
	for _, c := range cs {
		m.cursors[c.ID] = c
		if c.TokenHash != "" {
			m.tokens[c.TokenHash] = c.ID
		}
		if c.Launch != nil && !c.Status.Terminal() && c.LaunchPid > 0 {
			c.Status = CursorFailed
			c.Error = "agent process lost on daemon restart"
			now := time.Now()
			c.Ended = &now
			st.Put("cursors", c.ID, c)
		}
	}
	wts, _ := LoadAll[Worktree](st, "worktrees")
	for _, w := range wts {
		m.worktrees[w.ID] = w
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	go m.schedule()
	return m, nil
}

func lookupXUser(name string, appsUid int) (int, int, error) {
	if name == "" {
		return 0, 0, errors.New("no X user configured")
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, fmt.Errorf("X user %q: %w (create it with: useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin %s)", name, err, name)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if uid == appsUid {
		return 0, 0, fmt.Errorf("X user %q is the apps user; it must be a separate user", name)
	}
	return uid, gid, nil
}

func (m *Manager) Config() Config { return m.cfg }

func (m *Manager) RTC() *stream.RTC { return m.rtc }

func (m *Manager) Cgroups() bool { return m.cg != nil }

func (m *Manager) SandboxDefault() bool { return m.sandboxed }

func (m *Manager) probeSandbox() error {
	if m.uid == 0 {
		return errors.New("the sandbox needs the daemon to run as root with an apps user")
	}
	self := m.cfg.AgoraBinary
	if self == "" {
		self, _ = os.Executable()
	}
	return sandbox.Probe(self, m.uid, m.gid)
}

func (m *Manager) Shutdown() {
	m.cancel()
	m.mu.Lock()
	ids := make([]string, 0, len(m.rt))
	for id := range m.rt {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			m.Suspend(context.Background(), id, "shutdown")
		}(id)
	}
	wg.Wait()
	m.mu.Lock()
	for _, l := range m.launches {
		l.kill()
	}
	m.mu.Unlock()
	waitGroup(&m.agents, 7*time.Second)
	m.store.Flush()
}

func waitGroup(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

func (m *Manager) wsLock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.wsLocks[id]
	if l == nil {
		l = &sync.Mutex{}
		m.wsLocks[id] = l
	}
	return l
}

func (m *Manager) saveWS(w *Workspace) {
	w.Updated = time.Now().UTC()
	m.store.Put("workspaces", w.ID, w)
}

func (m *Manager) saveCursor(c *Cursor) {
	c.Updated = time.Now().UTC()
	m.store.Put("cursors", c.ID, c)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (m *Manager) CursorForToken(tok string) *Cursor {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.tokens[hashToken(tok)]
	if id == "" {
		return nil
	}
	c := m.cursors[id]
	if c == nil || c.Status.Terminal() {
		return nil
	}
	cp := *c
	return &cp
}

func (m *Manager) profileDir(name string) string {
	return filepath.Join(m.cfg.Home, "profiles", name)
}

func (m *Manager) wsDir(id string) string {
	return filepath.Join(m.cfg.Home, "workspaces", id)
}

func (m *Manager) chownApps(p string) {
	if m.uid != 0 {
		os.Lchown(p, m.uid, m.gid)
	}
}
