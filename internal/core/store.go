package core

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Store struct {
	dir      string
	mu       sync.Mutex
	pending  map[string]storeOp
	failed   map[string]storeOp
	running  bool
	backoff  time.Duration
	retryAt  time.Time
	kick     chan struct{}
	settled  chan struct{}
	onError  func(kind, id string, err error)
	flushMax time.Duration
}

type storeOp struct {
	kind string
	id   string
	data []byte
	del  bool
}

const (
	storeMinBackoff = 100 * time.Millisecond
	storeMaxBackoff = 30 * time.Second
)

func NewStore(dir string) (*Store, error) {
	for _, k := range []string{"workspaces", "cursors", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(dir, k), 0700); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, pending: map[string]storeOp{}, failed: map[string]storeOp{}, kick: make(chan struct{}, 1), settled: make(chan struct{}), flushMax: 5 * time.Second}, nil
}

func (s *Store) path(kind, id string) string {
	return filepath.Join(s.dir, kind, id+".json")
}

func (s *Store) Put(kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.enqueue(storeOp{kind: kind, id: id, data: b})
	return nil
}

func (s *Store) Delete(kind, id string) error {
	s.enqueue(storeOp{kind: kind, id: id, del: true})
	return nil
}

func (s *Store) Flush() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	if len(s.failed) > 0 {
		s.retryAt = time.Now()
	}
	ch := s.settled
	s.mu.Unlock()
	s.wake()
	t := time.NewTimer(s.flushMax)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
		log.Printf("store: flush gave up after %v", s.flushMax)
	}
}

func (s *Store) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Store) enqueue(op storeOp) {
	k := op.kind + "/" + op.id
	s.mu.Lock()
	s.pending[k] = op
	delete(s.failed, k)
	if !s.running {
		s.running = true
		go s.run()
	}
	s.mu.Unlock()
	s.wake()
}

func (s *Store) run() {
	for {
		s.mu.Lock()
		batch := s.pending
		s.pending = map[string]storeOp{}
		retrying := len(s.failed) > 0 && !time.Now().Before(s.retryAt)
		if retrying {
			for k, op := range s.failed {
				batch[k] = op
			}
			s.failed = map[string]storeOp{}
		}
		if len(batch) == 0 {
			close(s.settled)
			s.settled = make(chan struct{})
			if len(s.failed) == 0 {
				s.running = false
				s.mu.Unlock()
				return
			}
			wait := time.Until(s.retryAt)
			s.mu.Unlock()
			t := time.NewTimer(wait)
			select {
			case <-t.C:
			case <-s.kick:
			}
			t.Stop()
			continue
		}
		s.mu.Unlock()
		failed := map[string]storeOp{}
		dirs := map[string]bool{}
		for k, op := range batch {
			if err := s.apply(op); err != nil {
				s.fail(op, err)
				failed[k] = op
				continue
			}
			dirs[op.kind] = true
		}
		for k := range dirs {
			if d, err := os.Open(filepath.Join(s.dir, k)); err == nil {
				d.Sync()
				d.Close()
			}
		}
		s.mu.Lock()
		for k, op := range failed {
			if _, newer := s.pending[k]; !newer {
				s.failed[k] = op
			}
		}
		switch {
		case len(failed) > 0 && (retrying || s.backoff == 0):
			s.backoff = min(max(s.backoff*2, storeMinBackoff), storeMaxBackoff)
			s.retryAt = time.Now().Add(s.backoff)
		case len(s.failed) == 0:
			s.backoff = 0
		}
		s.mu.Unlock()
	}
}

func (s *Store) apply(op storeOp) error {
	p := s.path(op.kind, op.id)
	if op.del {
		err := os.Remove(p)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(op.data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *Store) fail(op storeOp, err error) {
	verb := "write"
	if op.del {
		verb = "delete"
	}
	log.Printf("store: %s %s/%s: %v", verb, op.kind, op.id, err)
	if s.onError != nil {
		s.onError(op.kind, op.id, err)
	}
}

func LoadAll[T any](s *Store, kind string) ([]*T, error) {
	ents, err := os.ReadDir(filepath.Join(s.dir, kind))
	if err != nil {
		return nil, err
	}
	out := []*T{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, kind, e.Name()))
		if err != nil {
			continue
		}
		v := new(T)
		if json.Unmarshal(b, v) == nil {
			out = append(out, v)
		}
	}
	return out, nil
}
