package x11

import (
	"strings"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type Window struct {
	ID     uint32 `json:"id"`
	Title  string `json:"title"`
	Class  string `json:"class"`
	Pid    int    `json:"pid"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Mapped bool   `json:"mapped"`
}

func atom(c *xgb.Conn, name string) xproto.Atom {
	r, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	return r.Atom
}

func propString(c *xgb.Conn, w xproto.Window, prop, typ xproto.Atom) string {
	r, err := xproto.GetProperty(c, false, w, prop, typ, 0, 1024).Reply()
	if err != nil || r == nil {
		return ""
	}
	return string(r.Value)
}

func propCard(c *xgb.Conn, w xproto.Window, prop xproto.Atom) int {
	r, err := xproto.GetProperty(c, false, w, prop, xproto.AtomCardinal, 0, 1).Reply()
	if err != nil || r == nil || len(r.Value) < 4 {
		return 0
	}
	return int(xgb.Get32(r.Value))
}

func (d *Display) Windows() ([]Window, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.conn
	tree, err := xproto.QueryTree(c, d.root).Reply()
	if err != nil {
		return nil, err
	}
	netName := atom(c, "_NET_WM_NAME")
	utf8 := atom(c, "UTF8_STRING")
	pidA := atom(c, "_NET_WM_PID")
	out := []Window{}
	for _, w := range tree.Children {
		attr, err := xproto.GetWindowAttributes(c, w).Reply()
		if err != nil || attr.OverrideRedirect || attr.Class != xproto.WindowClassInputOutput {
			continue
		}
		if attr.MapState != xproto.MapStateViewable {
			continue
		}
		g, err := xproto.GetGeometry(c, xproto.Drawable(w)).Reply()
		if err != nil || g.Width <= 1 || g.Height <= 1 {
			continue
		}
		title := propString(c, w, netName, utf8)
		if title == "" {
			title = propString(c, w, xproto.AtomWmName, xproto.AtomString)
		}
		class := propString(c, w, xproto.AtomWmClass, xproto.AtomString)
		if i := strings.IndexByte(class, 0); i >= 0 {
			rest := strings.TrimRight(class[i+1:], "\x00")
			if rest != "" {
				class = rest
			} else {
				class = class[:i]
			}
		}
		out = append(out, Window{ID: uint32(w), Title: title, Class: class, Pid: propCard(c, w, pidA), X: int(g.X), Y: int(g.Y), Width: int(g.Width), Height: int(g.Height), Mapped: true})
	}
	return out, nil
}

func (d *Display) WindowAt(x, y int) uint32 {
	ws, err := d.Windows()
	if err != nil {
		return 0
	}
	for i := len(ws) - 1; i >= 0; i-- {
		w := ws[i]
		if x >= w.X && y >= w.Y && x < w.X+w.Width && y < w.Y+w.Height {
			return w.ID
		}
	}
	return 0
}
