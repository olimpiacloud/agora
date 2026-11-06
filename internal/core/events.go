package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Seq       int64          `json:"seq"`
	Time      time.Time      `json:"time"`
	Type      string         `json:"type"`
	Workspace string         `json:"workspace,omitempty"`
	Cursor    string         `json:"cursor,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type Filter struct {
	Workspace string
	Cursor    string
	Types     []string
	Since     int64
}

func (f Filter) Match(e *Event) bool {
	if e.Seq <= f.Since {
		return false
	}
	if f.Workspace != "" && e.Workspace != f.Workspace {
		return false
	}
	if f.Cursor != "" && e.Cursor != f.Cursor {
		return false
	}
	if len(f.Types) == 0 {
		return true
	}
	for _, t := range f.Types {
		if t == e.Type || (strings.HasSuffix(t, "*") && strings.HasPrefix(e.Type, strings.TrimSuffix(t, "*"))) {
			return true
		}
	}
	return false
}

type sub struct {
	f  Filter
	ch chan *Event
}

type Bus struct {
	mu     sync.Mutex
	seq    int64
	ring   []*Event
	size   int
	subs   map[*sub]struct{}
	logDir string
	logs   map[string]*os.File
	quiet  map[string]bool
}

func NewBus(logDir string, size int) *Bus {
	os.MkdirAll(logDir, 0700)
	return &Bus{size: size, subs: map[*sub]struct{}{}, logDir: logDir, logs: map[string]*os.File{}, quiet: map[string]bool{"cursor.pointer": true, "stream.stats": true}}
}

func (b *Bus) Publish(typ, ws, cursor string, data map[string]any) *Event {
	b.mu.Lock()
	b.seq++
	e := &Event{Seq: b.seq, Time: time.Now().UTC(), Type: typ, Workspace: ws, Cursor: cursor, Data: data}
	if !b.quiet[typ] {
		b.ring = append(b.ring, e)
		if len(b.ring) > b.size {
			b.ring = b.ring[len(b.ring)-b.size:]
		}
		b.audit(e)
	}
	subs := make([]*sub, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		if s.f.Match(e) {
			select {
			case s.ch <- e:
			default:
			}
		}
	}
	return e
}

func (b *Bus) audit(e *Event) {
	key := e.Workspace
	if key == "" {
		key = "_system"
	}
	f := b.logs[key]
	if f == nil {
		var err error
		f, err = os.OpenFile(filepath.Join(b.logDir, key+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return
		}
		b.logs[key] = f
	}
	line, _ := json.Marshal(e)
	f.Write(append(line, '\n'))
}

func (b *Bus) CloseLog(ws string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if f := b.logs[ws]; f != nil {
		f.Close()
		delete(b.logs, ws)
	}
}

func (b *Bus) Subscribe(f Filter, buf int) (<-chan *Event, func()) {
	s := &sub{f: f, ch: make(chan *Event, buf)}
	b.mu.Lock()
	for _, e := range b.ring {
		if f.Since > 0 && f.Match(e) {
			select {
			case s.ch <- e:
			default:
			}
		}
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
	}
}

func (b *Bus) Recent(f Filter, limit int) []*Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []*Event{}
	for i := len(b.ring) - 1; i >= 0 && len(out) < limit; i-- {
		if f.Match(b.ring[i]) {
			out = append(out, b.ring[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (b *Bus) Seq() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}
