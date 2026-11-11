package core

import (
	"context"
	"errors"
	"image"
	"strings"
	"sync"
	"time"

	"github.com/olimpiacloud/agora/internal/x11"
)

type Shot struct {
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Cursor    string    `json:"cursor,omitempty"`
	Mime      string    `json:"mime"`
	Time      time.Time `json:"time"`
	Data      []byte    `json:"-"`
	seq       uint64
}

const (
	shotsPerWorkspace     = 64
	shotBytesPerWorkspace = 24 << 20
	shotBytesTotal        = 256 << 20
)

type shotStore struct {
	mu      sync.Mutex
	ws      map[string][]*Shot
	wsBytes map[string]int
	bytes   int
	seq     uint64
}

func (s *shotStore) put(sh *Shot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws == nil {
		s.ws = map[string][]*Shot{}
		s.wsBytes = map[string]int{}
	}
	s.seq++
	sh.seq = s.seq
	s.ws[sh.Workspace] = append(s.ws[sh.Workspace], sh)
	s.wsBytes[sh.Workspace] += len(sh.Data)
	s.bytes += len(sh.Data)
	w := sh.Workspace
	for len(s.ws[w]) > 1 && (len(s.ws[w]) > shotsPerWorkspace || s.wsBytes[w] > shotBytesPerWorkspace) {
		s.evict(w)
	}
	for s.bytes > shotBytesTotal {
		oldest := ""
		var seq uint64
		for ws, list := range s.ws {
			if list[0] != sh && (oldest == "" || list[0].seq < seq) {
				oldest, seq = ws, list[0].seq
			}
		}
		if oldest == "" {
			break
		}
		s.evict(oldest)
	}
}

func (s *shotStore) evict(ws string) {
	list := s.ws[ws]
	s.wsBytes[ws] -= len(list[0].Data)
	s.bytes -= len(list[0].Data)
	list[0] = nil
	if len(list) == 1 {
		delete(s.ws, ws)
		delete(s.wsBytes, ws)
		return
	}
	s.ws[ws] = list[1:]
}

func (s *shotStore) get(id string) *Shot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, list := range s.ws {
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].ID == id {
				return list[i]
			}
		}
	}
	return nil
}

func (s *shotStore) drop(ws string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytes -= s.wsBytes[ws]
	delete(s.ws, ws)
	delete(s.wsBytes, ws)
}

func (m *Manager) StoreShot(ws, cursor, mime string, data []byte) string {
	id := NewID("shot")
	m.shots.put(&Shot{ID: id, Workspace: ws, Cursor: cursor, Mime: mime, Time: time.Now().UTC(), Data: data})
	m.Bus.Publish("desktop.screenshot", ws, cursor, map[string]any{"shot": id, "bytes": len(data)})
	return id
}

func (m *Manager) GetShot(id string) *Shot { return m.shots.get(id) }

func (m *Manager) LiveDisplay(ctx context.Context, ws string) (*x11.Display, error) {
	r, err := m.ensureRuntime(ctx, ws, true)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	d := r.display
	r.mu.Unlock()
	if d == nil {
		return nil, errors.New("workspace has no display")
	}
	return d, nil
}

func (m *Manager) CursorRegion(ctx context.Context, ws, cursorID string) (image.Rectangle, error) {
	c, err := m.GetCursor(cursorID)
	if err != nil {
		return image.Rectangle{}, err
	}
	if c.Workspace != ws {
		return image.Rectangle{}, ErrNotFound
	}
	r := m.runtime(ws)
	if r == nil {
		return image.Rectangle{}, nil
	}
	if target, ok := strings.CutPrefix(c.Surface, "tab:"); ok {
		r.brMu.Lock()
		b := r.browser
		r.brMu.Unlock()
		if b != nil {
			var win struct {
				Bounds struct {
					Left        int    `json:"left"`
					Top         int    `json:"top"`
					Width       int    `json:"width"`
					Height      int    `json:"height"`
					WindowState string `json:"windowState"`
				} `json:"bounds"`
			}
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := b.CDP().Call(cctx, "", "Browser.getWindowForTarget", map[string]any{"targetId": target}, &win)
			cancel()
			if err == nil && win.Bounds.Width > 0 && win.Bounds.WindowState != "minimized" {
				bb := win.Bounds
				return image.Rect(bb.Left, bb.Top, bb.Left+bb.Width, bb.Top+bb.Height), nil
			}
		}
	}
	r.mu.Lock()
	d := r.display
	r.mu.Unlock()
	if d == nil || (c.X == 0 && c.Y == 0) {
		return image.Rectangle{}, nil
	}
	return WindowRegion(d, d.WindowAt(c.X, c.Y)), nil
}

func WindowRegion(d *x11.Display, id uint32) image.Rectangle {
	if id == 0 {
		return image.Rectangle{}
	}
	wins, err := d.Windows()
	if err != nil {
		return image.Rectangle{}
	}
	for _, w := range wins {
		if w.ID == id {
			return image.Rect(w.X, w.Y, w.X+w.Width, w.Y+w.Height)
		}
	}
	return image.Rectangle{}
}
