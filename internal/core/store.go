package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Store struct {
	dir string
}

func NewStore(dir string) (*Store, error) {
	for _, k := range []string{"workspaces", "cursors", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(dir, k), 0700); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(kind, id string) string {
	return filepath.Join(s.dir, kind, id+".json")
}

func (s *Store) Put(kind, id string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	p := s.path(kind, id)
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, p)
}

func (s *Store) Delete(kind, id string) error {
	err := os.Remove(s.path(kind, id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
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
