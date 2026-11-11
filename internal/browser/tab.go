package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Tab struct {
	TargetID    string `json:"target"`
	SessionID   string `json:"-"`
	ContextID   string `json:"context,omitempty"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Owner       string `json:"owner,omitempty"`
	ownsContext bool
	b           *Browser
	mu          sync.Mutex
	navMu       sync.Mutex
	lastUsed    time.Time
	loadCh      chan struct{}
	dialog      map[string]any
	dialogCh    chan struct{}
	chooser     map[string]any
	closed      bool
	dragCh      chan json.RawMessage
	emulated    bool
	toolbar     int
}

func (t *Tab) touch() {
	t.mu.Lock()
	t.lastUsed = time.Now()
	t.mu.Unlock()
}

func (t *Tab) LastUsed() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastUsed
}

func (t *Tab) SetOwner(owner string) {
	t.mu.Lock()
	t.Owner = owner
	t.mu.Unlock()
}

func (t *Tab) Info() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{"target": t.TargetID, "url": t.URL, "title": t.Title, "owner": t.Owner, "context": t.ContextID, "dialog": t.dialog, "file_chooser": t.chooser != nil, "last_used": t.lastUsed}
}

var ErrDialogOpen = errors.New("javascript dialog opened")

func (t *Tab) Dialog() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dialog == nil {
		return nil
	}
	d := make(map[string]any, len(t.dialog))
	for k, v := range t.dialog {
		d[k] = v
	}
	return d
}

func (t *Tab) DialogContext(ctx context.Context) (context.Context, context.CancelFunc) {
	t.mu.Lock()
	if t.dialogCh == nil {
		t.dialogCh = make(chan struct{})
	}
	ch := t.dialogCh
	t.mu.Unlock()
	dctx, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	go func() {
		select {
		case <-ch:
			cancel(ErrDialogOpen)
		case <-stop:
		case <-dctx.Done():
		}
	}()
	return dctx, func() {
		close(stop)
		cancel(nil)
	}
}

func (t *Tab) closeLocal() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
}

func (t *Tab) call(ctx context.Context, method string, params any, out any) error {
	t.touch()
	return t.b.cdp.Call(ctx, t.SessionID, method, params, out)
}

func (t *Tab) onEvent(ev Event) {
	switch ev.Method {
	case "Page.loadEventFired":
		select {
		case t.loadCh <- struct{}{}:
		default:
		}
		t.b.emit("browser.page.loaded", map[string]any{"target": t.TargetID})
	case "Page.javascriptDialogOpening":
		p := map[string]any{}
		json.Unmarshal(ev.Params, &p)
		p["target"] = t.TargetID
		t.mu.Lock()
		t.dialog = p
		if t.dialogCh != nil {
			close(t.dialogCh)
			t.dialogCh = nil
		}
		t.mu.Unlock()
		t.b.emit("browser.dialog.opened", p)
	case "Page.javascriptDialogClosed":
		t.mu.Lock()
		t.dialog = nil
		t.mu.Unlock()
		t.b.emit("browser.dialog.closed", map[string]any{"target": t.TargetID})
	case "Input.dragIntercepted":
		t.mu.Lock()
		ch := t.dragCh
		t.mu.Unlock()
		if ch != nil {
			select {
			case ch <- ev.Params:
			default:
			}
		}
	case "Page.fileChooserOpened":
		p := map[string]any{}
		json.Unmarshal(ev.Params, &p)
		p["target"] = t.TargetID
		t.mu.Lock()
		t.chooser = p
		t.mu.Unlock()
		t.b.emit("browser.filechooser.opened", p)
	}
}

func (t *Tab) Navigate(ctx context.Context, url string, wait string, timeout time.Duration) error {
	if err := AllowedURL(url); err != nil {
		return err
	}
	if url == "" {
		url = "about:blank"
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	t.navMu.Lock()
	defer t.navMu.Unlock()
	for len(t.loadCh) > 0 {
		<-t.loadCh
	}
	var r struct {
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	if err := t.call(ctx, "Page.navigate", map[string]any{"url": url}, &r); err != nil {
		return err
	}
	if r.ErrorText != "" {
		return fmt.Errorf("navigate: %s", r.ErrorText)
	}
	if wait == "none" || r.LoaderID == "" {
		return nil
	}
	if wait == "domcontentloaded" {
		return t.waitReady(ctx, timeout, "interactive")
	}
	select {
	case <-t.loadCh:
		return nil
	case <-time.After(timeout):
		return errors.New("timeout waiting for load")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Tab) waitReady(ctx context.Context, timeout time.Duration, state string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var s string
		if err := t.Eval(ctx, "document.readyState", &s); err == nil && (s == "complete" || (state == "interactive" && s == "interactive")) {
			return nil
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.New("timeout waiting for readyState")
}

func (t *Tab) Back(ctx context.Context) error {
	return t.history(ctx, -1)
}

func (t *Tab) Forward(ctx context.Context) error {
	return t.history(ctx, 1)
}

func (t *Tab) history(ctx context.Context, delta int) error {
	var h struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := t.call(ctx, "Page.getNavigationHistory", nil, &h); err != nil {
		return err
	}
	i := h.CurrentIndex + delta
	if i < 0 || i >= len(h.Entries) {
		return errors.New("no history entry")
	}
	return t.call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[i].ID}, nil)
}

func (t *Tab) Reload(ctx context.Context) error {
	return t.call(ctx, "Page.reload", nil, nil)
}

func (t *Tab) Eval(ctx context.Context, expr string, out any) error {
	var r struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
			Desc  string          `json:"description"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := t.call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true, "userGesture": true}, &r); err != nil {
		return err
	}
	if r.Exception != nil {
		d := r.Exception.Exception.Description
		if d == "" {
			d = r.Exception.Text
		}
		return fmt.Errorf("js: %s", d)
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

func (t *Tab) Screenshot(ctx context.Context, format string, quality int, full bool) ([]byte, error) {
	return t.ScreenshotScaled(ctx, format, quality, full, 0)
}

func (t *Tab) ScreenshotScaled(ctx context.Context, format string, quality int, full bool, maxWidth int) ([]byte, error) {
	if format == "" {
		format = "png"
	}
	p := map[string]any{"format": format, "captureBeyondViewport": full}
	t.mu.Lock()
	emu := t.emulated
	t.mu.Unlock()
	if emu && !full {
		var v [4]float64
		if err := t.Eval(ctx, "[scrollX, scrollY, innerWidth, innerHeight]", &v); err == nil && v[2] > 0 {
			scale := 1.0
			if maxWidth > 0 && v[2] > float64(maxWidth) {
				scale = float64(maxWidth) / v[2]
			}
			p["captureBeyondViewport"] = true
			p["clip"] = map[string]any{"x": v[0], "y": v[1], "width": v[2], "height": v[3], "scale": scale}
			maxWidth = 0
		}
	}
	if maxWidth > 0 {
		var dim [2]float64
		expr := "[innerWidth, innerHeight]"
		if full {
			expr = "[document.documentElement.scrollWidth, document.documentElement.scrollHeight]"
		}
		if err := t.Eval(ctx, expr, &dim); err == nil && dim[0] > float64(maxWidth) {
			p["clip"] = map[string]any{"x": 0, "y": 0, "width": dim[0], "height": dim[1], "scale": float64(maxWidth) / dim[0]}
		}
	}
	if format == "jpeg" || format == "webp" {
		if quality == 0 {
			quality = 70
		}
		p["quality"] = quality
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := t.call(ctx, "Page.captureScreenshot", p, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}

func (t *Tab) mouse(ctx context.Context, typ string, x, y float64, button string, clicks int, mods int) error {
	p := map[string]any{"type": typ, "x": x, "y": y, "modifiers": mods}
	if button != "" {
		p["button"] = button
		p["clickCount"] = clicks
	}
	return t.call(ctx, "Input.dispatchMouseEvent", p, nil)
}

func (t *Tab) Click(ctx context.Context, x, y float64, button string, clicks int) error {
	if button == "" {
		button = "left"
	}
	if clicks == 0 {
		clicks = 1
	}
	if err := t.mouse(ctx, "mouseMoved", x, y, "", 0, 0); err != nil {
		return err
	}
	for i := 1; i <= clicks; i++ {
		if err := t.mouse(ctx, "mousePressed", x, y, button, i, 0); err != nil {
			return err
		}
		if err := t.mouse(ctx, "mouseReleased", x, y, button, i, 0); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tab) Hover(ctx context.Context, x, y float64) error {
	return t.mouse(ctx, "mouseMoved", x, y, "", 0, 0)
}

func (t *Tab) Drag(ctx context.Context, x0, y0, x1, y1 float64, steps int) error {
	if steps < 2 {
		steps = 12
	}
	ch := make(chan json.RawMessage, 1)
	t.mu.Lock()
	t.dragCh = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.dragCh = nil
		t.mu.Unlock()
	}()
	if err := t.call(ctx, "Input.setInterceptDrags", map[string]any{"enabled": true}, nil); err != nil {
		return err
	}
	defer t.call(context.Background(), "Input.setInterceptDrags", map[string]any{"enabled": false}, nil)
	if err := t.mouse(ctx, "mouseMoved", x0, y0, "", 0, 0); err != nil {
		return err
	}
	if err := t.mouse(ctx, "mousePressed", x0, y0, "left", 1, 0); err != nil {
		return err
	}
	for i := 1; i <= steps; i++ {
		x := x0 + (x1-x0)*float64(i)/float64(steps)
		y := y0 + (y1-y0)*float64(i)/float64(steps)
		p := map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "left", "buttons": 1}
		if err := t.call(ctx, "Input.dispatchMouseEvent", p, nil); err != nil {
			return err
		}
		select {
		case raw := <-ch:
			var d struct {
				Data json.RawMessage `json:"data"`
			}
			json.Unmarshal(raw, &d)
			for _, typ := range []string{"dragEnter", "dragOver", "drop"} {
				if err := t.call(ctx, "Input.dispatchDragEvent", map[string]any{"type": typ, "x": x1, "y": y1, "data": d.Data}, nil); err != nil {
					return err
				}
			}
			return t.mouse(ctx, "mouseReleased", x1, y1, "left", 1, 0)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	return t.mouse(ctx, "mouseReleased", x1, y1, "left", 1, 0)
}

func (t *Tab) Scroll(ctx context.Context, x, y, dx, dy float64) error {
	return t.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": x, "y": y, "deltaX": dx, "deltaY": dy}, nil)
}

func (t *Tab) InsertText(ctx context.Context, text string) error {
	return t.call(ctx, "Input.insertText", map[string]any{"text": text}, nil)
}

func (t *Tab) TypeText(ctx context.Context, text string) error {
	for _, r := range text {
		s := string(r)
		if r == '\n' || r == '\t' {
			k := "Enter"
			if r == '\t' {
				k = "Tab"
			}
			if err := t.Press(ctx, k); err != nil {
				return err
			}
			continue
		}
		if err := t.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "text": s, "key": s, "unmodifiedText": s}, nil); err != nil {
			return err
		}
		if err := t.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": s}, nil); err != nil {
			return err
		}
	}
	return nil
}

type keyDef struct {
	key  string
	code string
	vk   int
	text string
}

var cdpKeys = map[string]keyDef{
	"enter": {"Enter", "Enter", 13, "\r"}, "tab": {"Tab", "Tab", 9, ""}, "escape": {"Escape", "Escape", 27, ""},
	"esc": {"Escape", "Escape", 27, ""}, "backspace": {"Backspace", "Backspace", 8, ""}, "delete": {"Delete", "Delete", 46, ""},
	"arrowleft": {"ArrowLeft", "ArrowLeft", 37, ""}, "arrowup": {"ArrowUp", "ArrowUp", 38, ""}, "arrowright": {"ArrowRight", "ArrowRight", 39, ""},
	"arrowdown": {"ArrowDown", "ArrowDown", 40, ""}, "left": {"ArrowLeft", "ArrowLeft", 37, ""}, "up": {"ArrowUp", "ArrowUp", 38, ""},
	"right": {"ArrowRight", "ArrowRight", 39, ""}, "down": {"ArrowDown", "ArrowDown", 40, ""}, "home": {"Home", "Home", 36, ""},
	"end": {"End", "End", 35, ""}, "pageup": {"PageUp", "PageUp", 33, ""}, "pagedown": {"PageDown", "PageDown", 34, ""},
	"space": {" ", "Space", 32, " "}, "f5": {"F5", "F5", 116, ""},
}

var cdpMods = map[string]int{"alt": 1, "ctrl": 2, "control": 2, "meta": 4, "cmd": 4, "super": 4, "shift": 8}

func (t *Tab) Press(ctx context.Context, combo string) error {
	parts := strings.Split(combo, "+")
	mods := 0
	var main string
	for _, p := range parts {
		if m, ok := cdpMods[strings.ToLower(p)]; ok {
			mods |= m
		} else {
			main = p
		}
	}
	kd, ok := cdpKeys[strings.ToLower(main)]
	if !ok {
		if len([]rune(main)) != 1 {
			return fmt.Errorf("unknown key %q", main)
		}
		up := strings.ToUpper(main)
		kd = keyDef{key: main, code: "Key" + up, vk: int([]rune(up)[0]), text: main}
		if mods&(1|2|4) != 0 {
			kd.text = ""
		}
	}
	down := map[string]any{"type": "keyDown", "key": kd.key, "code": kd.code, "windowsVirtualKeyCode": kd.vk, "modifiers": mods}
	if kd.text != "" {
		down["text"] = kd.text
	} else {
		down["type"] = "rawKeyDown"
	}
	if err := t.call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return t.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": kd.key, "code": kd.code, "windowsVirtualKeyCode": kd.vk, "modifiers": mods}, nil)
}

func (t *Tab) HandleDialog(ctx context.Context, accept bool, text string) error {
	p := map[string]any{"accept": accept}
	if text != "" {
		p["promptText"] = text
	}
	return t.call(ctx, "Page.handleJavaScriptDialog", p, nil)
}

func (t *Tab) Focus(ctx context.Context) error {
	return t.call(ctx, "Page.bringToFront", nil, nil)
}

func (t *Tab) Close(ctx context.Context) error {
	err := t.b.cdp.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": t.TargetID}, nil)
	if t.ownsContext && t.ContextID != "" {
		t.b.DisposeContext(ctx, t.ContextID)
	}
	return err
}

func (t *Tab) Emulate(ctx context.Context, w, h int, scale float64, mobile bool) error {
	if w == 0 || h == 0 {
		t.mu.Lock()
		t.emulated = false
		t.mu.Unlock()
		t.call(ctx, "Emulation.setTouchEmulationEnabled", map[string]any{"enabled": false}, nil)
		return t.call(ctx, "Emulation.clearDeviceMetricsOverride", nil, nil)
	}
	if scale == 0 {
		scale = 1
	}
	t.mu.Lock()
	t.emulated = true
	t.mu.Unlock()
	if err := t.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": h, "deviceScaleFactor": scale, "mobile": mobile}, nil); err != nil {
		return err
	}
	return t.call(ctx, "Emulation.setTouchEmulationEnabled", map[string]any{"enabled": mobile, "maxTouchPoints": 5}, nil)
}

func (t *Tab) ScreenPoint(ctx context.Context, x, y float64) (int, int, bool) {
	var o [4]float64
	if err := t.Eval(ctx, "[screenX, screenY, outerHeight - innerHeight, outerWidth]", &o); err != nil {
		return 0, 0, false
	}
	t.mu.Lock()
	emu := t.emulated
	if !emu && o[2] > 0 && o[2] < 200 {
		t.toolbar = int(o[2])
	}
	tb := t.toolbar
	t.mu.Unlock()
	if !emu {
		return int(o[0] + x), int(o[1] + o[2] + y), true
	}
	if tb == 0 {
		tb = 87
	}
	var win struct {
		Bounds struct {
			Left   int `json:"left"`
			Top    int `json:"top"`
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"bounds"`
	}
	if err := t.b.cdp.Call(ctx, "", "Browser.getWindowForTarget", map[string]any{"targetId": t.TargetID}, &win); err != nil {
		return 0, 0, false
	}
	bx := win.Bounds
	px := int(x)
	if px > bx.Width-12 {
		px = bx.Width - 12
	}
	py := int(y)
	if py > bx.Height-tb-12 {
		py = bx.Height - tb - 12
	}
	return bx.Left + px, bx.Top + tb + py, true
}

func (t *Tab) Settle(ctx context.Context) {
	select {
	case <-time.After(120 * time.Millisecond):
	case <-ctx.Done():
		return
	}
	t.waitReady(ctx, 4*time.Second, "interactive")
}
