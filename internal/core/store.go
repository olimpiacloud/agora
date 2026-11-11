package core

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Store struct {
	dir     string
	mu      sync.Mutex
	pending map[string]storeOp
	running bool
	idle    *sync.Cond
	onError func(kind, id string, err error)
}

type storeOp struct {
	kind string
	id   string
	data []byte
	del  bool
}

func NewStore(dir string) (*Store, error) {
	for _, k := range []string{"workspaces", "cursors", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(dir, k), 0700); err != nil {
			return nil, err
		}
	}
	s := &Store{dir: dir, pending: map[string]storeOp{}}
	s.idle = sync.NewCond(&s.mu)
	return s, nil
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
	for s.running {
		s.idle.Wait()
	}
	s.mu.Unlock()
}

func (s *Store) enqueue(op storeOp) {
	s.mu.Lock()
	s.pending[op.kind+"/"+op.id] = op
	if !s.running {
		s.running = true
		go s.run()
	}
	s.mu.Unlock()
}

func (s *Store) run() {
	for {
		s.mu.Lock()
		if len(s.pending) == 0 {
			s.running = false
			s.idle.Broadcast()
			s.mu.Unlock()
			return
		}
		batch := s.pending
		s.pending = map[string]storeOp{}
		s.mu.Unlock()
		dirs := map[string]bool{}
		for _, op := range batch {
			if err := s.apply(op); err != nil {
				s.fail(op, err)
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
