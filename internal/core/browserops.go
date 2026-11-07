package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/olimpiacloud/agora/internal/browser"
	"github.com/olimpiacloud/agora/internal/x11"
)

func xwin(id uint32) xproto.Window { return xproto.Window(id) }

func x11Layout(s string) x11.Layout { return x11.Layout(s) }

type OpenTabReq struct {
	Observe    bool   `json:"observe,omitempty"`
	URL        string `json:"url"`
	Isolated   bool   `json:"isolated,omitempty"`
	SameWindow bool   `json:"same_window,omitempty"`
}

func (m *Manager) browserFor(ctx context.Context, ws string) (*Runtime, *browser.Browser, error) {
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return nil, nil, err
	}
	r.touch()
	b, err := r.Browser(ctx)
	if err != nil {
		return nil, nil, err
	}
	return r, b, nil
}

func (m *Manager) OpenTab(ctx context.Context, ws, cursorID string, req OpenTabReq) (map[string]any, error) {
	c, err := m.actor(cursorID, "browser")
	if err != nil {
		return nil, err
	}
	if c != nil && c.Workspace != ws {
		return nil, fmt.Errorf("%w: cursor belongs to workspace %s", ErrForbidden, c.Workspace)
	}
	m.mu.Lock()
	w := m.ws[ws]
	maxTabs := 0
	if w != nil {
		maxTabs = w.Config.Budget.MaxTabs
	}
	m.mu.Unlock()
	r, b, err := m.browserFor(ctx, ws)
	if err != nil {
		return nil, err
	}
	if maxTabs > 0 && len(b.Targets()) >= maxTabs {
		return nil, fmt.Errorf("workspace tab limit reached (%d)", maxTabs)
	}
	tab, err := b.NewTab(ctx, browser.NewTabOptions{URL: req.URL, NewWindow: !req.SameWindow, Isolated: req.Isolated})
	if err != nil {
		return nil, err
	}
	owner := ""
	if c != nil {
		owner = c.ID
		tab.Owner = owner
		r.mu.Lock()
		r.tabOwner[tab.TargetID] = owner
		r.mu.Unlock()
		m.mu.Lock()
		if cc := m.cursors[c.ID]; cc != nil {
			cc.Surface = "tab:" + tab.TargetID
			m.saveCursor(cc)
		}
		m.mu.Unlock()
	}
	m.Bus.Publish("browser.tab.opened", ws, owner, map[string]any{"target": tab.TargetID, "url": req.URL, "isolated": req.Isolated})
	res := map[string]any{"target": tab.TargetID, "url": req.URL, "context": tab.ContextID}
	if req.Observe {
		tab.Settle(ctx)
		if s, e := tab.Observe(ctx, 900); e == nil {
			res["observation"] = r.observeDiff(tab.TargetID, s.Compact(false))
		}
	}
	return res, nil
}

func (m *Manager) ListTabs(ctx context.Context, ws string) ([]map[string]any, error) {
	r := m.runtime(ws)
	out := []map[string]any{}
	if r == nil {
		return out, nil
	}
	r.brMu.Lock()
	b := r.browser
	r.brMu.Unlock()
	if b == nil {
		return out, nil
	}
	b.Refresh(ctx)
	for _, t := range b.Targets() {
		r.mu.Lock()
		owner := r.tabOwner[t.TargetID]
		r.mu.Unlock()
		out = append(out, map[string]any{"target": t.TargetID, "url": t.URL, "title": t.Title, "owner": owner, "context": t.BrowserContextID})
	}
	return out, nil
}

func (m *Manager) tabFor(ctx context.Context, ws string, c *Cursor, target string) (*Runtime, *browser.Tab, error) {
	r, b, err := m.browserFor(ctx, ws)
	if err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	owner := r.tabOwner[target]
	r.mu.Unlock()
	if c != nil && owner != "" && owner != c.ID && !m.IsAncestor(c.ID, owner) {
		return nil, nil, fmt.Errorf("%w: tab is owned by cursor %s", ErrForbidden, owner)
	}
	tab := b.Tab(target)
	if tab == nil {
		found := false
		for _, t := range b.Targets() {
			if t.TargetID == target {
				found = true
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("tab %s: %w", target, ErrNotFound)
		}
		tab, err = b.Attach(ctx, target)
		if err != nil {
			return nil, nil, err
		}
	}
	if c != nil && owner == "" {
		r.mu.Lock()
		r.tabOwner[target] = c.ID
		r.mu.Unlock()
		tab.Owner = c.ID
	}
	return r, tab, nil
}

type TabAction struct {
	Action    string   `json:"action"`
	URL       string   `json:"url,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	X         float64  `json:"x,omitempty"`
	Y         float64  `json:"y,omitempty"`
	X2        float64  `json:"x2,omitempty"`
	Y2        float64  `json:"y2,omitempty"`
	Text      string   `json:"text,omitempty"`
	Keys      string   `json:"keys,omitempty"`
	Values    []string `json:"values,omitempty"`
	Files     []string `json:"files,omitempty"`
	DX        float64  `json:"dx,omitempty"`
	DY        float64  `json:"dy,omitempty"`
	Button    string   `json:"button,omitempty"`
	Count     int      `json:"count,omitempty"`
	Submit    bool     `json:"submit,omitempty"`
	Wait      string   `json:"wait,omitempty"`
	Selector  string   `json:"selector,omitempty"`
	Contains  string   `json:"url_contains,omitempty"`
	Accept    *bool    `json:"accept,omitempty"`
	Expr      string   `json:"expression,omitempty"`
	Format    string   `json:"format,omitempty"`
	Quality   int      `json:"quality,omitempty"`
	Full      bool     `json:"full_page,omitempty"`
	MaxText   int      `json:"max_text,omitempty"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
	Width     int      `json:"width,omitempty"`
	Height    int      `json:"height,omitempty"`
	Scale     float64  `json:"scale,omitempty"`
	Mobile    bool     `json:"mobile,omitempty"`
	MaxWidth  int      `json:"max_width,omitempty"`
	Observe   bool     `json:"observe,omitempty"`
	Boxes     bool     `json:"boxes,omitempty"`
}

var readOnlyTab = map[string]bool{"snapshot": true, "screenshot": true, "info": true, "challenge": true, "wait": true, "audit": true}

func (m *Manager) TabDo(ctx context.Context, ws, cursorID, target string, a TabAction) (map[string]any, error) {
	c, err := m.actor(cursorID, "browser")
	if err != nil {
		return nil, err
	}
	if c != nil && c.Workspace != ws {
		return nil, fmt.Errorf("%w: cursor belongs to workspace %s", ErrForbidden, c.Workspace)
	}
	holder, prio := "admin", PrioAdmin
	if c != nil {
		holder, prio = c.ID, PrioAgent
	}
	if !readOnlyTab[a.Action] {
		if err := m.Arb.Check(ws, "tab:"+target, holder, prio); err != nil {
			return nil, err
		}
	}
	r, tab, err := m.tabFor(ctx, ws, c, target)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(a.TimeoutMs) * time.Millisecond
	res := map[string]any{"ok": true}
	px, py := -1.0, -1.0
	if a.Ref != "" && c != nil {
		switch a.Action {
		case "click", "hover", "fill", "type", "select", "upload":
			if x, y, e := tab.RefPoint(ctx, a.Ref); e == nil {
				px, py = x, y
			}
		}
	}
	switch a.Action {
	case "navigate":
		err = tab.Navigate(ctx, a.URL, a.Wait, timeout)
	case "back":
		err = tab.Back(ctx)
	case "forward":
		err = tab.Forward(ctx)
	case "reload":
		err = tab.Reload(ctx)
	case "snapshot":
		maxText := a.MaxText
		if maxText == 0 {
			maxText = 3000
		}
		var s *browser.Snapshot
		s, err = tab.Snapshot(ctx, maxText, 0)
		if err == nil {
			res["snapshot"] = s.Compact(a.Boxes)
			res["url"], res["title"] = s.URL, s.Title
			if ch, _ := tab.DetectChallenge(ctx); ch != nil {
				res["challenge"] = ch
			}
		}
	case "screenshot":
		var b []byte
		b, err = tab.ScreenshotScaled(ctx, defStr(a.Format, "jpeg"), a.Quality, a.Full, a.MaxWidth)
		if err == nil {
			res["image"] = base64.StdEncoding.EncodeToString(b)
			res["mime"] = "image/" + defStr(a.Format, "jpeg")
			res["shot"] = m.StoreShot(ws, cursorID, "image/"+defStr(a.Format, "jpeg"), b)
			var vp [2]float64
			if e := tab.Eval(ctx, "[innerWidth, innerHeight]", &vp); e == nil {
				res["viewport"] = vp
			}
			res["full_page"] = a.Full
		}
	case "click":
		if a.Ref != "" {
			err = tab.ClickRef(ctx, a.Ref, a.Button, a.Count)
		} else {
			err = tab.Click(ctx, a.X, a.Y, a.Button, a.Count)
			px, py = a.X, a.Y
		}
	case "hover":
		if a.Ref != "" {
			err = tab.HoverRef(ctx, a.Ref)
		} else {
			err = tab.Hover(ctx, a.X, a.Y)
			px, py = a.X, a.Y
		}
	case "drag":
		err = tab.Drag(ctx, a.X, a.Y, a.X2, a.Y2, 16)
		px, py = a.X2, a.Y2
	case "fill":
		err = tab.FillRef(ctx, a.Ref, a.Text, a.Submit)
	case "type":
		if a.Ref != "" {
			if err = tab.ClickRef(ctx, a.Ref, "", 1); err != nil {
				break
			}
		}
		err = tab.TypeText(ctx, a.Text)
	case "insert_text":
		err = tab.InsertText(ctx, a.Text)
	case "press":
		err = tab.Press(ctx, a.Keys)
	case "scroll":
		x, y := a.X, a.Y
		if x == 0 && y == 0 {
			x, y = 200, 200
		}
		err = tab.Scroll(ctx, x, y, a.DX, a.DY)
		px, py = x, y
	case "select":
		err = tab.SelectRef(ctx, a.Ref, a.Values)
	case "upload":
		err = tab.SetFiles(ctx, a.Ref, a.Files)
	case "eval":
		var v any
		err = tab.Eval(ctx, a.Expr, &v)
		res["value"] = v
	case "wait":
		err = tab.WaitFor(ctx, a.Selector, a.Text, a.Contains, timeout)
	case "dialog":
		accept := true
		if a.Accept != nil {
			accept = *a.Accept
		}
		err = tab.HandleDialog(ctx, accept, a.Text)
	case "challenge":
		var ch *browser.Challenge
		ch, err = tab.DetectChallenge(ctx)
		res["challenge"] = ch
	case "audit":
		var r map[string]any
		r, err = tab.Audit(ctx)
		res["audit"] = r
	case "emulate":
		err = tab.Emulate(ctx, a.Width, a.Height, a.Scale, a.Mobile)
	case "focus":
		err = tab.Focus(ctx)
	case "info":
		res["tab"] = tab.Info()
	case "close":
		err = tab.Close(ctx)
		r.mu.Lock()
		delete(r.tabOwner, target)
		r.mu.Unlock()
	default:
		return nil, fmt.Errorf("unknown tab action %q", a.Action)
	}
	if err != nil {
		return nil, err
	}
	if a.Observe && !readOnlyTab[a.Action] && a.Action != "close" {
		tab.Settle(ctx)
		if s, e := tab.Observe(ctx, 900); e == nil {
			res["observation"] = r.observeDiff(target, s.Compact(false))
			if ch, _ := tab.DetectChallenge(ctx); ch != nil {
				res["challenge"] = ch
			}
		}
	}
	if !readOnlyTab[a.Action] && c != nil {
		sx, sy := -1, -1
		if px >= 0 {
			sx, sy = m.tabToScreen(ctx, r, tab, px, py)
		}
		info := map[string]any{"target": target}
		if a.Ref != "" {
			info["ref"] = a.Ref
		}
		if a.URL != "" {
			info["url"] = a.URL
		}
		if a.Text != "" {
			info["chars"] = len([]rune(a.Text))
		}
		m.noteAction(c, ws, "tab."+a.Action, sx, sy, info)
	}
	return res, nil
}

func (m *Manager) tabToScreen(ctx context.Context, r *Runtime, tab *browser.Tab, x, y float64) (int, int) {
	sx, sy, ok := tab.ScreenPoint(ctx, x, y)
	if !ok {
		return -1, -1
	}
	return sx, sy
}

func defStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (m *Manager) BrowserRestart(ctx context.Context, ws string) error {
	r := m.runtime(ws)
	if r == nil {
		return ErrState
	}
	r.brMu.Lock()
	b := r.browser
	r.browser = nil
	r.brMu.Unlock()
	if b == nil {
		return errors.New("browser not running")
	}
	tabs := r.tabRecordsFrom(b)
	b.Close(ctx)
	r.mu.Lock()
	r.lastTabs = tabs
	r.mu.Unlock()
	m.Bus.Publish("browser.stopped", ws, "", map[string]any{"tabs": len(tabs)})
	_, err := r.Browser(ctx)
	return err
}

func (r *Runtime) observeDiff(target, cur string) string {
	r.mu.Lock()
	if r.lastObs == nil {
		r.lastObs = map[string]string{}
	}
	prev := r.lastObs[target]
	r.lastObs[target] = cur
	r.mu.Unlock()
	if prev == "" {
		return cur
	}
	seen := map[string]bool{}
	prevText := ""
	if i := strings.Index(prev, "--- text ---\n"); i >= 0 {
		prevText = prev[i:]
		prev = prev[:i]
	}
	for _, l := range strings.Split(prev, "\n") {
		if strings.HasPrefix(l, "[") {
			seen[l] = true
		}
	}
	text := ""
	body := cur
	if i := strings.Index(cur, "--- text ---\n"); i >= 0 {
		text = cur[i:]
		body = cur[:i]
	}
	var b strings.Builder
	same := 0
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if strings.HasPrefix(l, "[") && seen[l] {
			same++
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if same > 0 {
		fmt.Fprintf(&b, "(%d elements unchanged since your last view of this tab)\n", same)
	}
	if text != "" && text == prevText {
		b.WriteString("--- text unchanged ---\n")
	} else {
		b.WriteString(text)
	}
	return b.String()
}
