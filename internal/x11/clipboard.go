package x11

import (
	"errors"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type Clipboard struct {
	d       *Display
	c       *xgb.Conn
	win     xproto.Window
	mu      sync.Mutex
	content string
	owned   bool
	clip    xproto.Atom
	primary xproto.Atom
	utf8    xproto.Atom
	targets xproto.Atom
	prop    xproto.Atom
	text    xproto.Atom
	reply   chan string
}

func (d *Display) NewClipboard() (*Clipboard, error) {
	c, err := d.Dial()
	if err != nil {
		return nil, err
	}
	id, _ := c.NewId()
	w := xproto.Window(id)
	if err := xproto.CreateWindowChecked(c, 0, w, d.root, -10, -10, 1, 1, 0, xproto.WindowClassInputOnly, 0, xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange}).Check(); err != nil {
		c.Close()
		return nil, err
	}
	cb := &Clipboard{d: d, c: c, win: w, clip: atom(c, "CLIPBOARD"), primary: xproto.AtomPrimary, utf8: atom(c, "UTF8_STRING"), targets: atom(c, "TARGETS"), prop: atom(c, "AGORA_SEL"), text: atom(c, "TEXT"), reply: make(chan string, 1)}
	go cb.loop()
	return cb, nil
}

func (cb *Clipboard) Close() { cb.c.Close() }

func (cb *Clipboard) Set(s string) error {
	cb.mu.Lock()
	cb.content = s
	cb.owned = true
	cb.mu.Unlock()
	for _, sel := range []xproto.Atom{cb.clip, cb.primary} {
		if err := xproto.SetSelectionOwnerChecked(cb.c, cb.win, sel, xproto.TimeCurrentTime).Check(); err != nil {
			return err
		}
	}
	return nil
}

func (cb *Clipboard) Get(timeout time.Duration) (string, error) {
	r, err := xproto.GetSelectionOwner(cb.c, cb.clip).Reply()
	if err != nil {
		return "", err
	}
	if r.Owner == cb.win {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		return cb.content, nil
	}
	if r.Owner == 0 {
		return "", nil
	}
	for len(cb.reply) > 0 {
		<-cb.reply
	}
	xproto.ConvertSelection(cb.c, cb.win, cb.clip, cb.utf8, cb.prop, xproto.TimeCurrentTime)
	select {
	case s := <-cb.reply:
		return s, nil
	case <-time.After(timeout):
		return "", errors.New("clipboard owner did not respond")
	}
}

func (cb *Clipboard) loop() {
	for {
		ev, err := cb.c.WaitForEvent()
		if ev == nil && err == nil {
			return
		}
		switch e := ev.(type) {
		case xproto.SelectionNotifyEvent:
			s := ""
			if e.Property != 0 {
				r, err := xproto.GetProperty(cb.c, true, cb.win, cb.prop, xproto.GetPropertyTypeAny, 0, 1<<20).Reply()
				if err == nil {
					s = string(r.Value)
				}
			}
			select {
			case cb.reply <- s:
			default:
			}
		case xproto.SelectionRequestEvent:
			cb.mu.Lock()
			content := cb.content
			cb.mu.Unlock()
			prop := e.Property
			if prop == 0 {
				prop = e.Target
			}
			switch e.Target {
			case cb.targets:
				b := make([]byte, 12)
				xgb.Put32(b, uint32(cb.targets))
				xgb.Put32(b[4:], uint32(cb.utf8))
				xgb.Put32(b[8:], uint32(xproto.AtomString))
				xproto.ChangeProperty(cb.c, xproto.PropModeReplace, e.Requestor, prop, xproto.AtomAtom, 32, 3, b)
			case cb.utf8, xproto.AtomString, cb.text:
				xproto.ChangeProperty(cb.c, xproto.PropModeReplace, e.Requestor, prop, e.Target, 8, uint32(len(content)), []byte(content))
			default:
				prop = 0
			}
			n := xproto.SelectionNotifyEvent{Time: e.Time, Requestor: e.Requestor, Selection: e.Selection, Target: e.Target, Property: prop}
			xproto.SendEvent(cb.c, false, e.Requestor, 0, string(n.Bytes()))
		case xproto.SelectionClearEvent:
			cb.mu.Lock()
			cb.owned = false
			cb.mu.Unlock()
		}
	}
}
