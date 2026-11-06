package core

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/agusgarcia3007/carbon/internal/stream"
	"github.com/agusgarcia3007/carbon/internal/x11"
)

type Viewer struct {
	ID        string
	Workspace string
	Control   bool
	Mode      stream.Mode
	sink      stream.Sink
	send      func([]byte) error
	r         *Runtime
	m         *Manager
	mu        sync.Mutex
	unsub     func()
	held      map[uint32]bool
	buttons   map[int]bool
	closed    bool
	sendMu    sync.Mutex
}

func (m *Manager) AttachViewer(ctx context.Context, ws, id string, mode stream.Mode, control bool, sink stream.Sink, send func([]byte) error) (*Viewer, error) {
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return nil, err
	}
	r.touch()
	r.mu.Lock()
	eng := r.engine
	r.mu.Unlock()
	if eng == nil {
		return nil, ErrState
	}
	v := &Viewer{ID: id, Workspace: ws, Control: control, Mode: mode, sink: sink, send: send, r: r, m: m, held: map[uint32]bool{}, buttons: map[int]bool{}}
	r.mu.Lock()
	r.viewers[id] = v
	n := len(r.viewers)
	r.mu.Unlock()
	if sink != nil {
		eng.AddSink(sink)
	}
	ch, unsub := m.Bus.Subscribe(Filter{Workspace: ws, Types: []string{"cursor.*", "control.*", "desktop.*", "browser.challenge.*", "human.*", "workspace.state", "stream.state"}}, 256)
	v.unsub = unsub
	go func() {
		for e := range ch {
			b, _ := json.Marshal(map[string]any{"type": "event", "event": e})
			if v.emit(b) != nil {
				return
			}
		}
	}()
	m.Bus.Publish("viewer.attached", ws, "", map[string]any{"viewer": id, "control": control, "mode": string(mode), "viewers": n})
	v.sendHello()
	return v, nil
}

func (v *Viewer) emit(b []byte) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	if v.send == nil {
		return nil
	}
	return v.send(b)
}

func (v *Viewer) SetSend(f func([]byte) error) {
	v.sendMu.Lock()
	v.send = f
	v.sendMu.Unlock()
	v.sendHello()
}

func (v *Viewer) sendHello() {
	m := v.m
	cs := m.ListCursors(CursorQuery{Workspace: v.Workspace, Active: true})
	w, h := 0, 0
	v.r.mu.Lock()
	if v.r.display != nil {
		w, h = v.r.display.Width, v.r.display.Height
	}
	v.r.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"type": "hello", "viewer": v.ID, "width": w, "height": h, "cursors": cs, "locks": m.Arb.List(v.Workspace), "control": v.Control})
	v.emit(b)
}

func (m *Manager) DetachViewer(ws, id string) {
	r := m.runtime(ws)
	if r == nil {
		return
	}
	r.mu.Lock()
	v := r.viewers[id]
	delete(r.viewers, id)
	eng := r.engine
	n := len(r.viewers)
	r.mu.Unlock()
	if v == nil {
		return
	}
	v.release()
	if v.unsub != nil {
		v.unsub()
	}
	if eng != nil && v.sink != nil {
		eng.RemoveSink(v.sink.ID())
	}
	if m.Arb.Release(ws, "workspace", "human:"+id) {
		m.Bus.Publish("control.returned", ws, "", map[string]any{"viewer": id, "reason": "viewer disconnected"})
		m.resumeNeedsHuman(ws)
	}
	m.Bus.Publish("viewer.detached", ws, "", map[string]any{"viewer": id, "viewers": n})
}

func (r *Runtime) closeViewers(reason string) {
	r.mu.Lock()
	ids := make([]string, 0, len(r.viewers))
	for id := range r.viewers {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.mu.Lock()
		v := r.viewers[id]
		r.mu.Unlock()
		if v != nil {
			b, _ := json.Marshal(map[string]any{"type": "bye", "reason": reason})
			v.emit(b)
		}
		r.m.DetachViewer(r.id, id)
	}
}

func (v *Viewer) release() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.r.mu.Lock()
	core := v.r.core
	v.r.mu.Unlock()
	if core == nil {
		return
	}
	for k := range v.held {
		core.Key(k, false)
	}
	for b := range v.buttons {
		core.Button(b, false)
	}
	v.held = map[uint32]bool{}
	v.buttons = map[int]bool{}
	v.closed = true
}

type viewerMsg struct {
	T    string  `json:"t"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	B    int     `json:"b"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
	Key  string  `json:"key"`
	Code string  `json:"code"`
	Down bool    `json:"down"`
	S    string  `json:"s"`
	W    int     `json:"w"`
	H    int     `json:"h"`
	Mode string  `json:"mode"`
}

func (v *Viewer) Input(raw []byte) {
	var msg viewerMsg
	if json.Unmarshal(raw, &msg) != nil {
		return
	}
	m := v.m
	switch msg.T {
	case "ping":
		b, _ := json.Marshal(map[string]any{"type": "pong", "s": msg.S})
		v.emit(b)
		return
	case "key_frame":
		v.r.mu.Lock()
		eng := v.r.engine
		v.r.mu.Unlock()
		if eng != nil {
			eng.RequestKeyframe()
		}
		return
	case "mode":
		if qs, ok := v.sink.(*stream.QueueSink); ok {
			if msg.Mode == string(stream.Interactive) {
				qs.SetMode(stream.Interactive)
			} else {
				qs.SetMode(stream.Observation)
			}
		}
		return
	}
	if !v.Control {
		return
	}
	v.r.touch()
	switch msg.T {
	case "take":
		if _, err := m.Arb.Acquire(v.Workspace, "workspace", "human:"+v.ID, PrioHuman, 0, "human takeover"); err == nil {
			m.Bus.Publish("control.taken", v.Workspace, "", map[string]any{"viewer": v.ID})
		}
		return
	case "release":
		if m.Arb.Release(v.Workspace, "workspace", "human:"+v.ID) {
			m.Bus.Publish("control.returned", v.Workspace, "", map[string]any{"viewer": v.ID})
			m.resumeNeedsHuman(v.Workspace)
		}
		return
	case "clip":
		m.Clipboard(context.Background(), v.Workspace, &msg.S)
		return
	case "clipget":
		s, _ := m.Clipboard(context.Background(), v.Workspace, nil)
		b, _ := json.Marshal(map[string]any{"type": "clipboard", "text": s})
		v.emit(b)
		return
	case "resize":
		go m.Resize(context.Background(), v.Workspace, msg.W, msg.H)
		return
	}
	held := m.Arb.List(v.Workspace)
	mine := false
	for _, l := range held {
		if l.Surface == "workspace" && l.Holder == "human:"+v.ID && l.Expires == nil {
			mine = true
		}
	}
	if !mine {
		m.Arb.Acquire(v.Workspace, "workspace", "human:"+v.ID, PrioHuman, 1500*time.Millisecond, "human input")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return
	}
	v.r.mu.Lock()
	core, d, wm := v.r.core, v.r.display, v.r.wm
	v.r.mu.Unlock()
	if core == nil || d == nil {
		return
	}
	x, y := int(msg.X), int(msg.Y)
	switch msg.T {
	case "move":
		core.Move(x, y)
	case "down":
		core.Move(x, y)
		if w := d.WindowAt(x, y); w != 0 {
			core.Focus(xwin(w))
			if wm != nil {
				wm.Raise(w)
			}
		}
		core.Button(btn(msg.B), true)
		v.buttons[btn(msg.B)] = true
	case "up":
		core.Move(x, y)
		core.Button(btn(msg.B), false)
		delete(v.buttons, btn(msg.B))
	case "wheel":
		core.Scroll(x, y, sign(msg.DX), sign(msg.DY))
	case "key":
		sym, err := x11.ParseKeysym(msg.Key)
		if err != nil {
			return
		}
		core.Key(sym, msg.Down)
		if msg.Down {
			v.held[sym] = true
		} else {
			delete(v.held, sym)
		}
	case "text":
		core.Type(msg.S, 0)
	}
}

func btn(b int) int {
	switch b {
	case 0:
		return 1
	case 1:
		return 2
	case 2:
		return 3
	}
	return b + 1
}

func sign(f float64) int {
	switch {
	case f > 0:
		return 1
	case f < 0:
		return -1
	}
	return 0
}

func (m *Manager) resumeNeedsHuman(ws string) {
	for _, c := range m.ListCursors(CursorQuery{Workspace: ws, Active: true}) {
		if c.Status == CursorNeedHuman {
			m.setCursorStatus(c.ID, CursorRunning, "human returned control", false)
		}
	}
}

func (m *Manager) RequestHuman(ws, cursorID, reason string) error {
	if cursorID != "" {
		if err := m.setCursorStatus(cursorID, CursorNeedHuman, reason, false); err != nil {
			return err
		}
	}
	m.Bus.Publish("human.requested", ws, cursorID, map[string]any{"reason": reason})
	return nil
}

func (m *Manager) TakeControl(ws, holder string) (*Lock, error) {
	l, err := m.Arb.Acquire(ws, "workspace", holder, PrioHuman, 0, "human takeover")
	if err == nil {
		m.Bus.Publish("control.taken", ws, "", map[string]any{"holder": holder})
	}
	return l, err
}

func (m *Manager) ReturnControl(ws, holder string) bool {
	ok := m.Arb.Release(ws, "workspace", holder)
	if ok {
		m.Bus.Publish("control.returned", ws, "", map[string]any{"holder": holder})
		m.resumeNeedsHuman(ws)
	}
	return ok
}

func (m *Manager) AcquireSurface(ws, cursorID, surface string, ttl time.Duration, reason string) (*Lock, error) {
	if _, err := m.actor(cursorID, ""); err != nil {
		return nil, err
	}
	l, err := m.Arb.Acquire(ws, surface, cursorID, PrioAgent, ttl, reason)
	if err == nil {
		m.Bus.Publish("control.acquired", ws, cursorID, map[string]any{"surface": surface, "ttl_ms": ttl.Milliseconds()})
	}
	return l, err
}

func (m *Manager) ReleaseSurface(ws, cursorID, surface string) bool {
	ok := m.Arb.Release(ws, surface, cursorID)
	if ok {
		m.Bus.Publish("control.released", ws, cursorID, map[string]any{"surface": surface})
	}
	return ok
}
