package core

import (
	"context"
	"errors"
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
}

type shotRing struct {
	mu    sync.Mutex
	list  []*Shot
	bytes int
}

func (s *shotRing) put(sh *Shot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, sh)
	s.bytes += len(sh.Data)
	for len(s.list) > 1 && (len(s.list) > 128 || s.bytes > 48<<20) {
		s.bytes -= len(s.list[0].Data)
		s.list = s.list[1:]
	}
}

func (s *shotRing) get(id string) *Shot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.list) - 1; i >= 0; i-- {
		if s.list[i].ID == id {
			return s.list[i]
		}
	}
	return nil
}

var shots shotRing

func (m *Manager) StoreShot(ws, cursor, mime string, data []byte) string {
	id := NewID("shot")
	shots.put(&Shot{ID: id, Workspace: ws, Cursor: cursor, Mime: mime, Time: time.Now().UTC(), Data: data})
	m.Bus.Publish("desktop.screenshot", ws, cursor, map[string]any{"shot": id, "bytes": len(data)})
	return id
}

func (m *Manager) GetShot(id string) *Shot { return shots.get(id) }

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
