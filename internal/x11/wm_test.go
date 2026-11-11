package x11

import (
	"context"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
)

func TestFullscreenToggle(t *testing.T) {
	requireXvfb(t)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	wm, err := d.StartWM(LayoutTile)
	if err != nil {
		t.Fatal(err)
	}
	defer wm.Close()
	c, err := d.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mk := func() xproto.Window {
		id, _ := c.NewId()
		w := xproto.Window(id)
		xproto.CreateWindow(c, 0, w, d.Root(), 0, 0, 100, 100, 0, xproto.WindowClassInputOutput, 0, 0, nil)
		xproto.MapWindow(c, w)
		return w
	}
	a := mk()
	mk()
	width := func(w xproto.Window) int {
		g, err := xproto.GetGeometry(c, xproto.Drawable(w)).Reply()
		if err != nil {
			t.Fatal(err)
		}
		return int(g.Width)
	}
	waitWidth := func(w xproto.Window, want int) {
		for i := 0; i < 100; i++ {
			if width(w) == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("width %d, want %d", width(w), want)
	}
	waitWidth(a, 320)
	state, fs := atom(c, "_NET_WM_STATE"), atom(c, "_NET_WM_STATE_FULLSCREEN")
	send := func(action uint32) {
		ev := xproto.ClientMessageEvent{Format: 32, Window: a, Type: state, Data: xproto.ClientMessageDataUnionData32New([]uint32{action, uint32(fs), 0, 1, 0})}
		xproto.SendEvent(c, false, d.Root(), xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify, string(ev.Bytes()))
		xproto.GetInputFocus(c).Reply()
	}
	send(2)
	waitWidth(a, 640)
	send(2)
	waitWidth(a, 320)
	send(1)
	waitWidth(a, 640)
	send(1)
	time.Sleep(100 * time.Millisecond)
	waitWidth(a, 640)
	send(0)
	waitWidth(a, 320)
}
