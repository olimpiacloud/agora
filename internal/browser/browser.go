package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/olimpiacloud/agora/internal/safefs"
	"github.com/olimpiacloud/agora/internal/sandbox"
)

type LaunchOptions struct {
	Binary      string
	UserDataDir string
	Env         []string
	Uid         uint32
	Gid         uint32
	Headless    bool
	Width       int
	Height      int
	ExtraArgs   []string
	DownloadDir string
	LogPath     string
	StartURLs   []string
	CgroupFD    int
	Wrap        []string
	OnEvent     func(kind string, data map[string]any)
}

type TargetInfo struct {
	TargetID         string `json:"targetId"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	URL              string `json:"url"`
	Attached         bool   `json:"attached"`
	OpenerID         string `json:"openerId,omitempty"`
	BrowserContextID string `json:"browserContextId,omitempty"`
}

type Browser struct {
	opts      LaunchOptions
	cmd       *exec.Cmd
	cdp       *CDP
	mu        sync.Mutex
	targets   map[string]*TargetInfo
	tabs      map[string]*Tab
	sessions  map[string]*Tab
	attaching map[string]chan struct{}
	contexts  map[string]bool
	exited    chan struct{}
	started   time.Time
}

func AllowedURL(u string) error {
	if u == "" || strings.EqualFold(u, "about:blank") {
		return nil
	}
	p, err := url.Parse(u)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", u, err)
	}
	switch p.Scheme {
	case "http", "https":
		if p.Host == "" {
			return fmt.Errorf("invalid URL %q: missing host", u)
		}
		return nil
	case "data":
		return nil
	}
	return fmt.Errorf("URL %q not allowed: only http, https, data and about:blank", u)
}

func DefaultBinary() string {
	for _, b := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(b); err == nil {
			return p
		}
	}
	return "google-chrome"
}

func Launch(ctx context.Context, o LaunchOptions) (*Browser, error) {
	if o.Binary == "" {
		o.Binary = DefaultBinary()
	}
	if o.Width == 0 {
		o.Width = 1280
	}
	if o.Height == 0 {
		o.Height = 800
	}
	if err := os.MkdirAll(o.UserDataDir, 0700); err != nil {
		return nil, err
	}
	if o.Uid != 0 {
		os.Lchown(o.UserDataDir, int(o.Uid), int(o.Gid))
	}
	disableTranslate(o.UserDataDir, o.Uid, o.Gid)
	if lock, err := os.Readlink(filepath.Join(o.UserDataDir, "SingletonLock")); err == nil {
		parts := strings.Split(lock, "-")
		if len(parts) > 1 {
			var pid int
			fmt.Sscanf(parts[len(parts)-1], "%d", &pid)
			if pid > 0 && len(o.Wrap) == 0 && syscall.Kill(pid, 0) == nil {
				return nil, fmt.Errorf("profile %s is in use by pid %d", o.UserDataDir, pid)
			}
		}
		os.Remove(filepath.Join(o.UserDataDir, "SingletonLock"))
		os.Remove(filepath.Join(o.UserDataDir, "SingletonSocket"))
		os.Remove(filepath.Join(o.UserDataDir, "SingletonCookie"))
	}
	args := []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + o.UserDataDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--password-store=basic",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-features=Translate,MediaRouter,OptimizationHints,AutofillServerCommunication",
		"--metrics-recording-only",
		"--disable-crash-reporter",
		"--disable-breakpad",
		"--hide-crash-restore-bubble",
		fmt.Sprintf("--window-size=%d,%d", o.Width, o.Height),
		"--window-position=0,0",
	}
	if o.Headless {
		args = append(args, "--headless=new")
	} else {
		args = append(args, "--ozone-platform=x11")
	}
	args = append(args, o.ExtraArgs...)
	if len(o.StartURLs) > 0 {
		args = append(args, o.StartURLs...)
	} else if o.Headless {
		args = append(args, "about:blank")
	} else {
		args = append(args, "--no-startup-window")
	}
	toChrome, ourW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	ourR, fromChrome, err := os.Pipe()
	if err != nil {
		toChrome.Close()
		ourW.Close()
		return nil, err
	}
	cmd := exec.Command(o.Binary, args...)
	attr := &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if len(o.Wrap) > 0 {
		cmd = exec.Command(o.Wrap[0], append(append(o.Wrap[1:len(o.Wrap):len(o.Wrap)], o.Binary), args...)...)
		attr = sandbox.Attr()
	} else if o.Uid != 0 && uint32(os.Getuid()) != o.Uid {
		attr.Credential = &syscall.Credential{Uid: o.Uid, Gid: o.Gid}
	}
	cmd.ExtraFiles = []*os.File{toChrome, fromChrome}
	cmd.Env = o.Env
	cmd.Dir = o.UserDataDir
	if o.CgroupFD > 0 {
		attr.UseCgroupFD = true
		attr.CgroupFD = o.CgroupFD
	}
	cmd.SysProcAttr = attr
	if o.LogPath != "" {
		if f, err := os.OpenFile(o.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600); err == nil {
			cmd.Stdout = f
			cmd.Stderr = f
			defer f.Close()
		}
	}
	err = cmd.Start()
	toChrome.Close()
	fromChrome.Close()
	if err != nil {
		ourW.Close()
		ourR.Close()
		return nil, err
	}
	b := newBrowser(o, NewCDP(ourR, ourW))
	b.cmd = cmd
	go func() {
		cmd.Wait()
		ourW.Close()
		ourR.Close()
		close(b.exited)
	}()
	b.cdp.Subscribe(b.onEvent)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var ver struct {
		Product string `json:"product"`
	}
	if err := b.cdp.Call(cctx, "", "Browser.getVersion", nil, &ver); err != nil {
		b.Kill()
		return nil, fmt.Errorf("browser handshake: %w", err)
	}
	if err := b.cdp.Call(cctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		b.Kill()
		return nil, err
	}
	if o.DownloadDir != "" {
		b.cdp.Call(cctx, "", "Browser.setDownloadBehavior", map[string]any{"behavior": "allow", "downloadPath": o.DownloadDir, "eventsEnabled": true}, nil)
	}
	var tl struct {
		TargetInfos []TargetInfo `json:"targetInfos"`
	}
	if err := b.cdp.Call(cctx, "", "Target.getTargets", nil, &tl); err == nil {
		b.mu.Lock()
		for i := range tl.TargetInfos {
			t := tl.TargetInfos[i]
			b.targets[t.TargetID] = &t
		}
		b.mu.Unlock()
	}
	return b, nil
}

func newBrowser(o LaunchOptions, c *CDP) *Browser {
	return &Browser{opts: o, cdp: c, targets: map[string]*TargetInfo{}, tabs: map[string]*Tab{}, sessions: map[string]*Tab{}, attaching: map[string]chan struct{}{}, contexts: map[string]bool{}, exited: make(chan struct{}), started: time.Now()}
}

func (b *Browser) emit(kind string, data map[string]any) {
	if b.opts.OnEvent != nil {
		b.opts.OnEvent(kind, data)
	}
}

func (b *Browser) onEvent(ev Event) {
	switch ev.Method {
	case "Target.targetCreated", "Target.targetInfoChanged":
		var p struct {
			TargetInfo TargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(ev.Params, &p) != nil {
			return
		}
		b.mu.Lock()
		old, existed := b.targets[p.TargetInfo.TargetID]
		t := p.TargetInfo
		b.targets[t.TargetID] = &t
		if tab := b.tabs[t.TargetID]; tab != nil {
			tab.mu.Lock()
			tab.URL, tab.Title = t.URL, t.Title
			tab.mu.Unlock()
		}
		b.mu.Unlock()
		if t.Type == "page" {
			if !existed {
				b.emit("browser.tab.created", map[string]any{"target": t.TargetID, "url": t.URL, "opener": t.OpenerID})
			} else if old.URL != t.URL {
				b.emit("browser.tab.navigated", map[string]any{"target": t.TargetID, "url": t.URL, "title": t.Title})
				b.checkChallenge(t.TargetID)
			}
		}
	case "Target.targetDestroyed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(ev.Params, &p)
		b.mu.Lock()
		t := b.targets[p.TargetID]
		delete(b.targets, p.TargetID)
		tab := b.tabs[p.TargetID]
		delete(b.tabs, p.TargetID)
		if tab != nil {
			delete(b.sessions, tab.SessionID)
		}
		b.mu.Unlock()
		if tab != nil {
			tab.closeLocal()
		}
		if t != nil && t.Type == "page" {
			b.emit("browser.tab.closed", map[string]any{"target": p.TargetID})
		}
	case "Browser.downloadWillBegin":
		var p map[string]any
		json.Unmarshal(ev.Params, &p)
		b.emit("browser.download.started", p)
	case "Browser.downloadProgress":
		var p map[string]any
		json.Unmarshal(ev.Params, &p)
		if s, _ := p["state"].(string); s == "completed" || s == "canceled" {
			b.emit("browser.download."+s, p)
		}
	default:
		if ev.SessionID != "" {
			b.mu.Lock()
			tab := b.sessions[ev.SessionID]
			b.mu.Unlock()
			if tab != nil {
				tab.onEvent(ev)
			}
		}
	}
}

func (b *Browser) CDP() *CDP { return b.cdp }

func (b *Browser) Pid() int {
	if b.cmd != nil && b.cmd.Process != nil {
		return b.cmd.Process.Pid
	}
	return 0
}

func (b *Browser) Exited() <-chan struct{} { return b.exited }

func (b *Browser) Refresh(ctx context.Context) {
	var tl struct {
		TargetInfos []TargetInfo `json:"targetInfos"`
	}
	if err := b.cdp.Call(ctx, "", "Target.getTargets", nil, &tl); err != nil {
		return
	}
	b.mu.Lock()
	seen := map[string]bool{}
	for i := range tl.TargetInfos {
		t := tl.TargetInfos[i]
		seen[t.TargetID] = true
		b.targets[t.TargetID] = &t
		if tab := b.tabs[t.TargetID]; tab != nil {
			tab.mu.Lock()
			tab.URL, tab.Title = t.URL, t.Title
			tab.mu.Unlock()
		}
	}
	for id := range b.targets {
		if !seen[id] {
			delete(b.targets, id)
		}
	}
	b.mu.Unlock()
}

func (b *Browser) Targets() []TargetInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []TargetInfo{}
	for _, t := range b.targets {
		if t.Type == "page" {
			out = append(out, *t)
		}
	}
	return out
}

type NewTabOptions struct {
	URL        string
	NewWindow  bool
	Isolated   bool
	ContextID  string
	Background bool
}

func (b *Browser) CreateContext(ctx context.Context) (string, error) {
	var r struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := b.cdp.Call(ctx, "", "Target.createBrowserContext", map[string]any{"disposeOnDetach": false}, &r); err != nil {
		return "", err
	}
	b.mu.Lock()
	b.contexts[r.BrowserContextID] = true
	b.mu.Unlock()
	return r.BrowserContextID, nil
}

func (b *Browser) DisposeContext(ctx context.Context, id string) error {
	b.mu.Lock()
	delete(b.contexts, id)
	b.mu.Unlock()
	return b.cdp.Call(ctx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": id}, nil)
}

func (b *Browser) NewTab(ctx context.Context, o NewTabOptions) (*Tab, error) {
	if err := AllowedURL(o.URL); err != nil {
		return nil, err
	}
	if o.URL == "" {
		o.URL = "about:blank"
	}
	created := false
	if o.Isolated && o.ContextID == "" {
		id, err := b.CreateContext(ctx)
		if err != nil {
			return nil, err
		}
		o.ContextID = id
		created = true
	}
	cleanup := func(targetID string) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if targetID != "" {
			b.cdp.Call(cctx, "", "Target.closeTarget", map[string]any{"targetId": targetID}, nil)
		}
		if created {
			b.DisposeContext(cctx, o.ContextID)
		}
	}
	p := map[string]any{"url": o.URL, "newWindow": o.NewWindow, "background": o.Background}
	if o.ContextID != "" {
		p["browserContextId"] = o.ContextID
	}
	var r struct {
		TargetID string `json:"targetId"`
	}
	if err := b.cdp.Call(ctx, "", "Target.createTarget", p, &r); err != nil {
		cleanup("")
		return nil, err
	}
	t, err := b.attach(ctx, r.TargetID, o.ContextID, o.Isolated)
	if err != nil {
		cleanup(r.TargetID)
		return nil, err
	}
	return t, nil
}

func (b *Browser) Tab(id string) *Tab {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tabs[id]
}

func (b *Browser) Tabs() []*Tab {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Tab, 0, len(b.tabs))
	for _, t := range b.tabs {
		out = append(out, t)
	}
	return out
}

func (b *Browser) Attach(ctx context.Context, targetID string) (*Tab, error) {
	return b.attach(ctx, targetID, "", false)
}

func (b *Browser) attach(ctx context.Context, targetID, contextID string, ownsContext bool) (*Tab, error) {
	for {
		b.mu.Lock()
		if t := b.tabs[targetID]; t != nil {
			b.mu.Unlock()
			return t, nil
		}
		wait := b.attaching[targetID]
		if wait == nil {
			done := make(chan struct{})
			b.attaching[targetID] = done
			b.mu.Unlock()
			t, err := b.attachSession(ctx, targetID, contextID, ownsContext)
			b.mu.Lock()
			delete(b.attaching, targetID)
			if err == nil {
				if ti := b.targets[targetID]; ti != nil {
					t.mu.Lock()
					t.URL, t.Title = ti.URL, ti.Title
					t.mu.Unlock()
				}
				b.tabs[targetID] = t
			}
			b.mu.Unlock()
			close(done)
			return t, err
		}
		b.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (b *Browser) attachSession(ctx context.Context, targetID, contextID string, ownsContext bool) (*Tab, error) {
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.cdp.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, &r); err != nil {
		return nil, err
	}
	t := &Tab{TargetID: targetID, SessionID: r.SessionID, ContextID: contextID, ownsContext: ownsContext, b: b, lastUsed: time.Now(), loadCh: make(chan struct{}, 1)}
	b.mu.Lock()
	b.sessions[r.SessionID] = t
	b.mu.Unlock()
	if err := b.cdp.Call(ctx, r.SessionID, "Page.enable", nil, nil); err != nil {
		b.mu.Lock()
		delete(b.sessions, r.SessionID)
		b.mu.Unlock()
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		b.cdp.Call(cctx, "", "Target.detachFromTarget", map[string]any{"sessionId": r.SessionID}, nil)
		cancel()
		return nil, err
	}
	b.cdp.Call(ctx, r.SessionID, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}, nil)
	return t, nil
}

func (b *Browser) Close(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b.cdp.Call(cctx, "", "Browser.close", nil, nil)
	select {
	case <-b.exited:
		return nil
	case <-time.After(8 * time.Second):
		b.Kill()
		return errors.New("browser did not exit, killed")
	}
}

func (b *Browser) Kill() {
	if b.cmd != nil && b.cmd.Process != nil {
		syscall.Kill(-b.cmd.Process.Pid, syscall.SIGKILL)
	}
	select {
	case <-b.exited:
	case <-time.After(3 * time.Second):
	}
}

func (b *Browser) checkChallenge(targetID string) {
	t := b.Tab(targetID)
	if t == nil {
		return
	}
	go func() {
		time.Sleep(1200 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if c, err := t.DetectChallenge(ctx); err == nil && c != nil {
			b.emit("browser.challenge.detected", map[string]any{"target": targetID, "kind": c.Kind, "url": c.URL, "box": c.Box})
		}
	}()
}

func disableTranslate(dir string, uid, gid uint32) {
	p := filepath.Join("Default", "Preferences")
	prefs := map[string]any{}
	if b, err := safefs.ReadFileBeneath(dir, p); err == nil {
		if json.Unmarshal(b, &prefs) != nil {
			return
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return
	}
	tr, _ := prefs["translate"].(map[string]any)
	if tr == nil {
		tr = map[string]any{}
	}
	if v, ok := tr["enabled"].(bool); ok && !v {
		return
	}
	tr["enabled"] = false
	prefs["translate"] = tr
	b, err := json.Marshal(prefs)
	if err != nil {
		return
	}
	owner, group := -1, -1
	if uid != 0 {
		owner, group = int(uid), int(gid)
	}
	if safefs.MkdirBeneath(dir, "Default", 0700) != nil {
		return
	}
	if safefs.WriteFileBeneath(dir, p, b, 0600, owner, group) == nil {
		safefs.ChownBeneath(dir, "Default", owner, group)
	}
}
