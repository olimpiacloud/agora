package core

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type storeItem struct {
	ID string `json:"id"`
	N  int    `json:"n"`
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Flush)
	return s
}

func TestStorePutFlushLoad(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := s.Put("cursors", fmt.Sprintf("c%d", i), storeItem{ID: fmt.Sprintf("c%d", i), N: i}); err != nil {
			t.Fatal(err)
		}
	}
	s.Flush()
	got, err := LoadAll[storeItem](s, "cursors")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d items", len(got))
	}
	for _, it := range got {
		if it.ID != fmt.Sprintf("c%d", it.N) {
			t.Fatalf("bad item %+v", it)
		}
	}
	ents, _ := os.ReadDir(filepath.Join(s.dir, "cursors"))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover %s", e.Name())
		}
	}
}

func TestStoreDeleteAfterPutWins(t *testing.T) {
	s := newTestStore(t)
	s.Put("workspaces", "w", storeItem{ID: "w"})
	s.Delete("workspaces", "w")
	s.Flush()
	if _, err := os.Stat(s.path("workspaces", "w")); !os.IsNotExist(err) {
		t.Fatalf("deleted object still on disk: %v", err)
	}
	s.Put("workspaces", "w", storeItem{ID: "w", N: 1})
	s.Flush()
	s.Delete("workspaces", "w")
	s.Flush()
	if _, err := os.Stat(s.path("workspaces", "w")); !os.IsNotExist(err) {
		t.Fatalf("delete after a flushed put left the file: %v", err)
	}
}

func TestStoreCoalescesPuts(t *testing.T) {
	s := newTestStore(t)
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	for i := 0; i < 100; i++ {
		s.Put("cursors", "c", storeItem{ID: "c", N: i})
	}
	s.mu.Lock()
	n := len(s.pending)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d pending writes for one key", n)
	}
	go s.run()
	s.Flush()
	got, _ := LoadAll[storeItem](s, "cursors")
	if len(got) != 1 || got[0].N != 99 {
		t.Fatalf("got %+v", got)
	}
}

func TestStoreWriteErrorIsReported(t *testing.T) {
	s := newTestStore(t)
	if err := os.RemoveAll(filepath.Join(s.dir, "cursors")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "cursors"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	var failed []string
	s.onError = func(kind, id string, err error) {
		failed = append(failed, kind+"/"+id)
	}
	s.Put("cursors", "c", storeItem{ID: "c"})
	s.Put("workspaces", "w", storeItem{ID: "w"})
	s.Flush()
	if len(failed) != 1 || failed[0] != "cursors/c" {
		t.Fatalf("errors reported for %v", failed)
	}
	if !strings.Contains(buf.String(), "store: write cursors/c") {
		t.Fatalf("log output %q", buf.String())
	}
	if _, err := os.Stat(s.path("workspaces", "w")); err != nil {
		t.Fatalf("other writes stopped after an error: %v", err)
	}
}
