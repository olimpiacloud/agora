package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"time"

	"github.com/olimpiacloud/agora/internal/x11"
)

type InputReq struct {
	Action  string `json:"action"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	X2      int    `json:"x2,omitempty"`
	Y2      int    `json:"y2,omitempty"`
	Button  int    `json:"button,omitempty"`
	Count   int    `json:"count,omitempty"`
	Text    string `json:"text,omitempty"`
	Keys    string `json:"keys,omitempty"`
	DX      int    `json:"dx,omitempty"`
	DY      int    `json:"dy,omitempty"`
	Window  uint32 `json:"window,omitempty"`
	NoRaise bool   `json:"no_raise,omitempty"`
	DelayMs int    `json:"delay_ms,omitempty"`
}

func (m *Manager) actor(cursorID, perm string) (*Cursor, error) {
	if cursorID == "" {
		return nil, nil
	}
	c, err := m.GetCursor(cursorID)
	if err != nil {
		return nil, err
	}
	if c.Status.Terminal() {
		return nil, fmt.Errorf("cursor is %s", c.Status)
	}
	if c.Status == CursorPaused {
		return nil, errors.New("cursor is paused")
	}
	if perm != "" && !c.Can(perm) {
		return nil, fmt.Errorf("%w: cursor lacks %q permission", ErrForbidden, perm)
	}
	return c, nil
}

func (m *Manager) noteAction(c *Cursor, ws, action string, x, y int, data map[string]any) {
	if c == nil {
		return
	}
	m.mu.Lock()
	cc := m.cursors[c.ID]
	if cc != nil {
		cc.Actions++
		cc.LastAction = action
		if x >= 0 && y >= 0 {
			cc.X, cc.Y = x, y
		}
		if cc.Status == CursorPending {
			cc.Status = CursorRunning
		}
		cc.Updated = time.Now().UTC()
	}
	m.mu.Unlock()
	if data == nil {
		data = map[string]any{}
	}
	data["action"] = action
	if x >= 0 {
		data["x"], data["y"] = x, y
	}
	m.Bus.Publish("cursor.pointer", ws, c.ID, map[string]any{"x": x, "y": y, "action": action, "color": c.Color, "name": c.Name})
	m.Bus.Publish("cursor.action", ws, c.ID, data)
}

func (m *Manager) DesktopInput(ctx context.Context, ws, cursorID string, req InputReq) (map[string]any, error) {
	c, err := m.actor(cursorID, "desktop")
	if err != nil {
		return nil, err
	}
	if c != nil && c.Workspace != ws {
		return nil, fmt.Errorf("%w: cursor belongs to workspace %s", ErrForbidden, c.Workspace)
	}
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return nil, err
	}
	r.touch()
	r.mu.Lock()
	d, wm := r.display, r.wm
	r.mu.Unlock()
	if d == nil {
		return nil, errors.New("workspace has no display (headless)")
	}
	var seat *x11.Seat
	holder, prio := "admin", PrioAdmin
	if c != nil {
		holder, prio = c.ID, PrioAgent
		seat, err = r.seatFor(c.ID)
	} else {
		r.mu.Lock()
		seat = r.core
		r.mu.Unlock()
	}
	if err != nil {
		return nil, err
	}
	pointer := map[string]bool{"move": true, "click": true, "double_click": true, "right_click": true, "middle_click": true, "down": true, "up": true, "drag": true, "scroll": true}
	win := req.Window
	if pointer[req.Action] && win == 0 {
		win = d.WindowAt(req.X, req.Y)
	}
	if !pointer[req.Action] && win == 0 {
		if f, err := seat.Focused(); err == nil && uint32(f) > 1 && uint32(f) != uint32(d.Root()) {
			win = uint32(f)
		}
	}
	surface := ""
	if win != 0 {
		surface = fmt.Sprintf("win:%d", win)
	}
	if err := m.Arb.Check(ws, surface, holder, prio); err != nil {
		return nil, err
	}
	focus := func() {
		if win != 0 {
			seat.Focus(xwin(win))
			if !req.NoRaise && wm != nil {
				wm.Raise(win)
			}
		}
	}
	delay := time.Duration(req.DelayMs) * time.Millisecond
	switch req.Action {
	case "move":
		err = seat.Move(req.X, req.Y)
	case "click", "double_click", "right_click", "middle_click":
		b, n := req.Button, req.Count
		switch req.Action {
		case "double_click":
			n = 2
		case "right_click":
			b = 3
		case "middle_click":
			b = 2
		}
		focus()
		err = seat.Click(req.X, req.Y, b, n)
	case "down":
		focus()
		seat.Move(req.X, req.Y)
		err = seat.Button(max(req.Button, 1), true)
	case "up":
		seat.Move(req.X, req.Y)
		err = seat.Button(max(req.Button, 1), false)
	case "drag":
		focus()
		err = seat.Drag(req.X, req.Y, req.X2, req.Y2, 16)
	case "scroll":
		err = seat.Scroll(req.X, req.Y, req.DX, req.DY)
	case "type":
		if win != 0 {
			seat.Focus(xwin(win))
		}
		err = seat.Type(req.Text, delay)
	case "key", "press":
		if win != 0 {
			seat.Focus(xwin(win))
		}
		err = seat.Press(req.Keys)
	default:
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
	if err != nil {
		return nil, err
	}
	x, y := seat.Position()
	info := map[string]any{"window": win, "seat": seat.Name}
	if req.Action == "type" {
		info["chars"] = len([]rune(req.Text))
	}
	if req.Action == "key" || req.Action == "press" {
		info["keys"] = req.Keys
	}
	m.noteAction(c, ws, req.Action, x, y, info)
	return map[string]any{"ok": true, "x": x, "y": y, "window": win}, nil
}

type ShotOpts struct {
	Window   uint32 `json:"window,omitempty"`
	X        int    `json:"x,omitempty"`
	Y        int    `json:"y,omitempty"`
	W        int    `json:"w,omitempty"`
	H        int    `json:"h,omitempty"`
	Format   string `json:"format,omitempty"`
	Quality  int    `json:"quality,omitempty"`
	MaxWidth int    `json:"max_width,omitempty"`
}

func (m *Manager) Screenshot(ctx context.Context, ws string, o ShotOpts) ([]byte, string, error) {
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return nil, "", err
	}
	r.mu.Lock()
	d := r.display
	r.mu.Unlock()
	if d == nil {
		return nil, "", errors.New("no display")
	}
	cp, err := d.NewCapturer()
	if err != nil {
		return nil, "", err
	}
	defer cp.Close()
	var f *x11.Frame
	switch {
	case o.Window != 0:
		f, err = cp.GrabWindow(o.Window, nil)
	case o.W > 0 && o.H > 0:
		f, err = cp.Grab(o.X, o.Y, o.W, o.H, nil)
	default:
		f, err = cp.GrabScreen(nil)
	}
	if err != nil {
		return nil, "", err
	}
	img := f.RGBA()
	var out image.Image = img
	if o.MaxWidth > 0 && img.Bounds().Dx() > o.MaxWidth {
		out = downscale(img, o.MaxWidth)
	}
	var buf bytes.Buffer
	if o.Format == "png" {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		err = enc.Encode(&buf, out)
		return buf.Bytes(), "image/png", err
	}
	q := o.Quality
	if q == 0 {
		q = 75
	}
	err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: q})
	return buf.Bytes(), "image/jpeg", err
}

func downscale(src *image.RGBA, maxW int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dw := maxW
	dh := sh * dw / sw
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy0 := y * sh / dh
		sy1 := (y + 1) * sh / dh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < dw; x++ {
			sx0 := x * sw / dw
			sx1 := (x + 1) * sw / dw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, b, n int
			for yy := sy0; yy < sy1; yy++ {
				o := yy*src.Stride + sx0*4
				for xx := sx0; xx < sx1; xx++ {
					r += int(src.Pix[o])
					g += int(src.Pix[o+1])
					b += int(src.Pix[o+2])
					o += 4
					n++
				}
			}
			p := y*dst.Stride + x*4
			dst.Pix[p] = byte(r / n)
			dst.Pix[p+1] = byte(g / n)
			dst.Pix[p+2] = byte(b / n)
			dst.Pix[p+3] = 255
		}
	}
	return dst
}

func (m *Manager) Windows(ctx context.Context, ws string) ([]x11.Window, error) {
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	d := r.display
	r.mu.Unlock()
	if d == nil {
		return []x11.Window{}, nil
	}
	return d.Windows()
}

type WindowReq struct {
	Action string `json:"action"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (m *Manager) WindowOp(ctx context.Context, ws, cursorID string, win uint32, req WindowReq) error {
	c, err := m.actor(cursorID, "desktop")
	if err != nil {
		return err
	}
	holder, prio := "admin", PrioAdmin
	if c != nil {
		holder, prio = c.ID, PrioAgent
	}
	if err := m.Arb.Check(ws, fmt.Sprintf("win:%d", win), holder, prio); err != nil {
		return err
	}
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return err
	}
	r.touch()
	if r.wm == nil {
		return ErrState
	}
	switch req.Action {
	case "raise", "focus":
		r.wm.Raise(win)
		if c != nil {
			if s, err := r.seatFor(c.ID); err == nil {
				s.Focus(xwin(win))
			}
		}
	case "move", "resize":
		r.wm.MoveResize(win, req.X, req.Y, req.Width, req.Height)
	case "tile", "release":
		r.wm.Release(win)
	case "close":
		r.wm.CloseWindow(win)
	default:
		return fmt.Errorf("unknown window action %q", req.Action)
	}
	m.Bus.Publish("desktop.window", ws, cursorID, map[string]any{"window": win, "action": req.Action})
	return nil
}

func (m *Manager) Clipboard(ctx context.Context, ws string, set *string) (string, error) {
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	cb := r.clip
	r.mu.Unlock()
	if cb == nil {
		return "", errors.New("clipboard unavailable")
	}
	if set != nil {
		return *set, cb.Set(*set)
	}
	return cb.Get(2 * time.Second)
}

func Downscale(src *image.RGBA, maxW int) *image.RGBA {
	if src.Bounds().Dx() <= maxW {
		return src
	}
	return downscale(src, maxW)
}
