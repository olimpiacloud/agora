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
	logs   map[string]*auditLog
	maxLog int64
	gone   map[string]bool
	goneQ  []string
	quiet  map[string]bool
}

const maxGoneLogs = 1024

type auditLog struct {
	f    *os.File
	size int64
}

var auditMaxBytes int64 = 16 << 20

func NewBus(logDir string, size int) *Bus {
	os.MkdirAll(logDir, 0700)
	return &Bus{size: size, subs: map[*sub]struct{}{}, logDir: logDir, logs: map[string]*auditLog{}, maxLog: auditMaxBytes, gone: map[string]bool{}, quiet: map[string]bool{"cursor.pointer": true, "stream.stats": true}}
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
	for s := range b.subs {
		if s.f.Match(e) {
			select {
			case s.ch <- e:
			default:
			}
		}
	}
	b.mu.Unlock()
	return e
}

func (b *Bus) audit(e *Event) {
	key := e.Workspace
	if key == "" {
		key = "_system"
	}
	if b.gone[key] {
		return
	}
	l := b.logs[key]
	if l == nil {
		f, err := os.OpenFile(b.logPath(key), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return
		}
		l = &auditLog{f: f}
		if fi, err := f.Stat(); err == nil {
			l.size = fi.Size()
		}
		b.logs[key] = l
	}
	line, _ := json.Marshal(e)
	n, _ := l.f.Write(append(line, '\n'))
	l.size += int64(n)
	if l.size > b.maxLog {
		l.f.Close()
		delete(b.logs, key)
		os.Rename(b.logPath(key), b.logPath(key)+".1")
	}
}

func (b *Bus) logPath(key string) string {
	return filepath.Join(b.logDir, key+".jsonl")
}

func (b *Bus) CloseLog(ws string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if l := b.logs[ws]; l != nil {
		l.f.Close()
		delete(b.logs, ws)
	}
}

func (b *Bus) RemoveLog(ws string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if l := b.logs[ws]; l != nil {
		l.f.Close()
		delete(b.logs, ws)
	}
	if !b.gone[ws] {
		b.gone[ws] = true
		b.goneQ = append(b.goneQ, ws)
		if len(b.goneQ) > maxGoneLogs {
			delete(b.gone, b.goneQ[0])
			b.goneQ = b.goneQ[1:]
		}
	}
	os.Remove(b.logPath(ws))
	os.Remove(b.logPath(ws) + ".1")
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
		if _, ok := b.subs[s]; ok {
			delete(b.subs, s)
			close(s.ch)
		}
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
