package x11

import (
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type Layout string

const (
	LayoutTile  Layout = "tile"
	LayoutMax   Layout = "max"
	LayoutFloat Layout = "float"
)

type WM struct {
	d        *Display
	c        *xgb.Conn
	mu       sync.Mutex
	layout   Layout
	clients  []xproto.Window
	trans    map[xproto.Window]xproto.Window
	fixed    map[xproto.Window]bool
	atoms    map[string]xproto.Atom
	check    xproto.Window
	OnChange func()
}

var wmAtomNames = []string{
	"WM_PROTOCOLS", "WM_DELETE_WINDOW", "WM_TRANSIENT_FOR", "_NET_SUPPORTED", "_NET_SUPPORTING_WM_CHECK",
	"_NET_WM_NAME", "UTF8_STRING", "_NET_CLIENT_LIST", "_NET_ACTIVE_WINDOW", "_NET_WM_STATE",
	"_NET_WM_STATE_FULLSCREEN", "_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ",
	"_NET_WM_WINDOW_TYPE", "_NET_WM_WINDOW_TYPE_DIALOG", "_NET_WM_WINDOW_TYPE_NORMAL", "_NET_CLOSE_WINDOW",
	"_NET_WORKAREA", "_NET_NUMBER_OF_DESKTOPS", "_NET_CURRENT_DESKTOP", "_NET_DESKTOP_GEOMETRY",
	"_NET_WM_PID", "_NET_FRAME_EXTENTS", "_NET_REQUEST_FRAME_EXTENTS", "_NET_WM_WINDOW_TYPE_SPLASH",
	"_NET_WM_WINDOW_TYPE_UTILITY",
}

func (d *Display) StartWM(layout Layout) (*WM, error) {
	c, err := d.Dial()
	if err != nil {
		return nil, err
	}
	w := &WM{d: d, c: c, layout: layout, trans: map[xproto.Window]xproto.Window{}, fixed: map[xproto.Window]bool{}, atoms: map[string]xproto.Atom{}}
	for _, n := range wmAtomNames {
		w.atoms[n] = atom(c, n)
	}
	err = xproto.ChangeWindowAttributesChecked(c, d.root, xproto.CwEventMask, []uint32{
		xproto.EventMaskSubstructureRedirect | xproto.EventMaskSubstructureNotify | xproto.EventMaskPropertyChange,
	}).Check()
	if err != nil {
		c.Close()
		return nil, err
	}
	cid, _ := c.NewId()
	w.check = xproto.Window(cid)
	xproto.CreateWindow(c, 0, w.check, d.root, -1, -1, 1, 1, 0, xproto.WindowClassInputOnly, 0, 0, nil)
	w.setWindowProp(d.root, "_NET_SUPPORTING_WM_CHECK", []uint32{uint32(w.check)})
	w.setWindowProp(w.check, "_NET_SUPPORTING_WM_CHECK", []uint32{uint32(w.check)})
	name := "agora"
	xproto.ChangeProperty(c, xproto.PropModeReplace, w.check, w.atoms["_NET_WM_NAME"], w.atoms["UTF8_STRING"], 8, uint32(len(name)), []byte(name))
	sup := []uint32{}
	for _, n := range []string{"_NET_SUPPORTED", "_NET_SUPPORTING_WM_CHECK", "_NET_CLIENT_LIST", "_NET_ACTIVE_WINDOW", "_NET_WM_STATE", "_NET_WM_STATE_FULLSCREEN", "_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_CLOSE_WINDOW", "_NET_WM_NAME", "_NET_WORKAREA", "_NET_FRAME_EXTENTS", "_NET_REQUEST_FRAME_EXTENTS"} {
		sup = append(sup, uint32(w.atoms[n]))
	}
	w.setAtomProp(d.root, "_NET_SUPPORTED", sup)
	w.setCardProp(d.root, "_NET_NUMBER_OF_DESKTOPS", []uint32{1})
	w.setCardProp(d.root, "_NET_CURRENT_DESKTOP", []uint32{0})
	w.updateGeometryProps()
	tree, err := xproto.QueryTree(c, d.root).Reply()
	if err == nil {
		for _, ch := range tree.Children {
			a, err := xproto.GetWindowAttributes(c, ch).Reply()
			if err == nil && !a.OverrideRedirect && a.MapState == xproto.MapStateViewable {
				w.manage(ch)
			}
		}
	}
	w.relayout()
	go w.loop()
	return w, nil
}

func (w *WM) setWindowProp(win xproto.Window, name string, v []uint32) {
	w.setProp(win, name, xproto.AtomWindow, v)
}

func (w *WM) setAtomProp(win xproto.Window, name string, v []uint32) {
	w.setProp(win, name, xproto.AtomAtom, v)
}

func (w *WM) setCardProp(win xproto.Window, name string, v []uint32) {
	w.setProp(win, name, xproto.AtomCardinal, v)
}

func (w *WM) setProp(win xproto.Window, name string, typ xproto.Atom, v []uint32) {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		xgb.Put32(b[i*4:], x)
	}
	xproto.ChangeProperty(w.c, xproto.PropModeReplace, win, w.atoms[name], typ, 32, uint32(len(v)), b)
}

func (w *WM) updateGeometryProps() {
	sw, sh := w.d.Width, w.d.Height
	w.setCardProp(w.d.root, "_NET_DESKTOP_GEOMETRY", []uint32{uint32(sw), uint32(sh)})
	w.setCardProp(w.d.root, "_NET_WORKAREA", []uint32{0, 0, uint32(sw), uint32(sh)})
}

func (w *WM) SetLayout(l Layout) {
	w.mu.Lock()
	w.layout = l
	w.mu.Unlock()
	w.relayout()
}

func (w *WM) Close() {
	xproto.DestroyWindow(w.c, w.check)
	w.c.Close()
}

func (w *WM) isTransient(win xproto.Window) (xproto.Window, bool) {
	r, err := xproto.GetProperty(w.c, false, win, xproto.AtomWmTransientFor, xproto.AtomWindow, 0, 1).Reply()
	if err == nil && r != nil && len(r.Value) >= 4 {
		return xproto.Window(xgb.Get32(r.Value)), true
	}
	r, err = xproto.GetProperty(w.c, false, win, w.atoms["_NET_WM_WINDOW_TYPE"], xproto.AtomAtom, 0, 8).Reply()
	if err == nil && r != nil {
		for i := 0; i+4 <= len(r.Value); i += 4 {
			a := xproto.Atom(xgb.Get32(r.Value[i:]))
			if a == w.atoms["_NET_WM_WINDOW_TYPE_DIALOG"] || a == w.atoms["_NET_WM_WINDOW_TYPE_SPLASH"] || a == w.atoms["_NET_WM_WINDOW_TYPE_UTILITY"] {
				return 0, true
			}
		}
	}
	return 0, false
}

func (w *WM) manage(win xproto.Window) {
	w.mu.Lock()
	for _, c := range w.clients {
		if c == win {
			w.mu.Unlock()
			return
		}
	}
	if p, ok := w.isTransient(win); ok {
		w.trans[win] = p
	} else {
		w.clients = append(w.clients, win)
	}
	w.mu.Unlock()
	xproto.ChangeWindowAttributes(w.c, win, xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange | xproto.EventMaskStructureNotify})
	w.setCardProp(win, "_NET_FRAME_EXTENTS", []uint32{0, 0, 0, 0})
	w.updateClientList()
}

func (w *WM) unmanage(win xproto.Window) {
	w.mu.Lock()
	delete(w.trans, win)
	delete(w.fixed, win)
	for i, c := range w.clients {
		if c == win {
			w.clients = append(w.clients[:i], w.clients[i+1:]...)
			break
		}
	}
	w.mu.Unlock()
	w.updateClientList()
}

func (w *WM) updateClientList() {
	w.mu.Lock()
	ids := make([]uint32, 0, len(w.clients)+len(w.trans))
	for _, c := range w.clients {
		ids = append(ids, uint32(c))
	}
	for t := range w.trans {
		ids = append(ids, uint32(t))
	}
	w.mu.Unlock()
	w.setWindowProp(w.d.root, "_NET_CLIENT_LIST", ids)
	if w.OnChange != nil {
		go w.OnChange()
	}
}

func (w *WM) relayout() {
	w.mu.Lock()
	layout := w.layout
	var tiles []xproto.Window
	for _, c := range w.clients {
		if !w.fixed[c] {
			tiles = append(tiles, c)
		}
	}
	trans := map[xproto.Window]xproto.Window{}
	for k, v := range w.trans {
		trans[k] = v
	}
	w.mu.Unlock()
	sw, sh := w.d.Width, w.d.Height
	mask := uint16(xproto.ConfigWindowX | xproto.ConfigWindowY | xproto.ConfigWindowWidth | xproto.ConfigWindowHeight | xproto.ConfigWindowBorderWidth)
	switch layout {
	case LayoutMax:
		for _, c := range tiles {
			xproto.ConfigureWindow(w.c, c, mask, []uint32{0, 0, uint32(sw), uint32(sh), 0})
		}
	case LayoutTile:
		n := len(tiles)
		if n == 0 {
			break
		}
		cols := 1
		for cols*cols < n {
			cols++
		}
		rows := (n + cols - 1) / cols
		for i, c := range tiles {
			r := i / cols
			col := i % cols
			inRow := cols
			if r == rows-1 && n%cols != 0 {
				inRow = n % cols
			}
			cw := sw / inRow
			ch := sh / rows
			xproto.ConfigureWindow(w.c, c, mask, []uint32{uint32(col * cw), uint32(r * ch), uint32(cw), uint32(ch), 0})
		}
	}
	for t, p := range trans {
		w.centerOver(t, p)
	}
	xproto.GetInputFocus(w.c).Reply()
}

func (w *WM) centerOver(t, p xproto.Window) {
	g, err := xproto.GetGeometry(w.c, xproto.Drawable(t)).Reply()
	if err != nil {
		return
	}
	px, py, pw, ph := 0, 0, w.d.Width, w.d.Height
	if p != 0 {
		if pg, err := xproto.GetGeometry(w.c, xproto.Drawable(p)).Reply(); err == nil {
			px, py, pw, ph = int(pg.X), int(pg.Y), int(pg.Width), int(pg.Height)
		}
	}
	x := px + (pw-int(g.Width))/2
	y := py + (ph-int(g.Height))/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	xproto.ConfigureWindow(w.c, t, xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowStackMode, []uint32{uint32(int32(x)), uint32(int32(y)), xproto.StackModeAbove})
}

func (w *WM) Raise(win uint32) {
	xproto.ConfigureWindow(w.c, xproto.Window(win), xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
	w.setWindowProp(w.d.root, "_NET_ACTIVE_WINDOW", []uint32{win})
	xproto.GetInputFocus(w.c).Reply()
}

func (w *WM) MoveResize(win uint32, x, y, width, height int) {
	w.mu.Lock()
	w.fixed[xproto.Window(win)] = true
	w.mu.Unlock()
	xproto.ConfigureWindow(w.c, xproto.Window(win), xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{uint32(int32(x)), uint32(int32(y)), uint32(width), uint32(height)})
	xproto.GetInputFocus(w.c).Reply()
}

func (w *WM) Release(win uint32) {
	w.mu.Lock()
	delete(w.fixed, xproto.Window(win))
	w.mu.Unlock()
	w.relayout()
}

func (w *WM) CloseWindow(win uint32) {
	xw := xproto.Window(win)
	r, err := xproto.GetProperty(w.c, false, xw, w.atoms["WM_PROTOCOLS"], xproto.AtomAtom, 0, 32).Reply()
	if err == nil && r != nil {
		for i := 0; i+4 <= len(r.Value); i += 4 {
			if xproto.Atom(xgb.Get32(r.Value[i:])) == w.atoms["WM_DELETE_WINDOW"] {
				ev := xproto.ClientMessageEvent{Format: 32, Window: xw, Type: w.atoms["WM_PROTOCOLS"], Data: xproto.ClientMessageDataUnionData32New([]uint32{uint32(w.atoms["WM_DELETE_WINDOW"]), 0, 0, 0, 0})}
				xproto.SendEvent(w.c, false, xw, xproto.EventMaskNoEvent, string(ev.Bytes()))
				xproto.GetInputFocus(w.c).Reply()
				return
			}
		}
	}
	xproto.KillClient(w.c, win)
	xproto.GetInputFocus(w.c).Reply()
}

func (w *WM) ScreenChanged() {
	w.updateGeometryProps()
	w.relayout()
}

func (w *WM) loop() {
	for {
		ev, err := w.c.WaitForEvent()
		if ev == nil && err == nil {
			return
		}
		switch e := ev.(type) {
		case xproto.MapRequestEvent:
			w.manage(e.Window)
			xproto.MapWindow(w.c, e.Window)
			w.relayout()
			w.Raise(uint32(e.Window))
		case xproto.ConfigureRequestEvent:
			w.mu.Lock()
			managed := false
			for _, c := range w.clients {
				if c == e.Window && !w.fixed[c] && w.layout != LayoutFloat {
					managed = true
				}
			}
			w.mu.Unlock()
			if managed {
				g, err := xproto.GetGeometry(w.c, xproto.Drawable(e.Window)).Reply()
				if err == nil {
					ce := xproto.ConfigureNotifyEvent{Event: e.Window, Window: e.Window, X: g.X, Y: g.Y, Width: g.Width, Height: g.Height}
					xproto.SendEvent(w.c, false, e.Window, xproto.EventMaskStructureNotify, string(ce.Bytes()))
				}
				continue
			}
			var vals []uint32
			if e.ValueMask&xproto.ConfigWindowX != 0 {
				vals = append(vals, uint32(int32(e.X)))
			}
			if e.ValueMask&xproto.ConfigWindowY != 0 {
				vals = append(vals, uint32(int32(e.Y)))
			}
			if e.ValueMask&xproto.ConfigWindowWidth != 0 {
				vals = append(vals, uint32(e.Width))
			}
			if e.ValueMask&xproto.ConfigWindowHeight != 0 {
				vals = append(vals, uint32(e.Height))
			}
			if e.ValueMask&xproto.ConfigWindowBorderWidth != 0 {
				vals = append(vals, uint32(e.BorderWidth))
			}
			if e.ValueMask&xproto.ConfigWindowSibling != 0 {
				vals = append(vals, uint32(e.Sibling))
			}
			if e.ValueMask&xproto.ConfigWindowStackMode != 0 {
				vals = append(vals, uint32(e.StackMode))
			}
			xproto.ConfigureWindow(w.c, e.Window, e.ValueMask, vals)
		case xproto.UnmapNotifyEvent:
			if e.Event == w.d.root {
				continue
			}
			w.unmanage(e.Window)
			w.relayout()
		case xproto.DestroyNotifyEvent:
			w.unmanage(e.Window)
			w.relayout()
		case xproto.ClientMessageEvent:
			switch e.Type {
			case w.atoms["_NET_ACTIVE_WINDOW"]:
				w.Raise(uint32(e.Window))
			case w.atoms["_NET_CLOSE_WINDOW"]:
				w.CloseWindow(uint32(e.Window))
			case w.atoms["_NET_REQUEST_FRAME_EXTENTS"]:
				w.setCardProp(e.Window, "_NET_FRAME_EXTENTS", []uint32{0, 0, 0, 0})
			case w.atoms["_NET_WM_STATE"]:
				d := e.Data.Data32
				fs := xproto.Atom(d[1]) == w.atoms["_NET_WM_STATE_FULLSCREEN"] || xproto.Atom(d[2]) == w.atoms["_NET_WM_STATE_FULLSCREEN"]
				if fs {
					on := d[0] == 1 || d[0] == 2
					if on {
						w.setAtomProp(e.Window, "_NET_WM_STATE", []uint32{uint32(w.atoms["_NET_WM_STATE_FULLSCREEN"])})
						w.MoveResize(uint32(e.Window), 0, 0, w.d.Width, w.d.Height)
						w.Raise(uint32(e.Window))
					} else {
						w.setAtomProp(e.Window, "_NET_WM_STATE", []uint32{})
						w.Release(uint32(e.Window))
					}
				}
			}
		}
	}
}
