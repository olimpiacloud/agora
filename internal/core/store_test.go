package core

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
	var bufMu sync.Mutex
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		bufMu.Lock()
		defer bufMu.Unlock()
		return buf.Write(p)
	}))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	var mu sync.Mutex
	failed := map[string]int{}
	s.onError = func(kind, id string, err error) {
		mu.Lock()
		failed[kind+"/"+id]++
		mu.Unlock()
	}
	s.Put("cursors", "c", storeItem{ID: "c"})
	s.Put("workspaces", "w", storeItem{ID: "w"})
	s.Flush()
	mu.Lock()
	if len(failed) != 1 || failed["cursors/c"] == 0 {
		t.Fatalf("errors reported for %v", failed)
	}
	mu.Unlock()
	bufMu.Lock()
	out := buf.String()
	bufMu.Unlock()
	if !strings.Contains(out, "store: write cursors/c") {
		t.Fatalf("log output %q", out)
	}
	if _, err := os.Stat(s.path("workspaces", "w")); err != nil {
		t.Fatalf("other writes stopped after an error: %v", err)
	}
	os.Remove(filepath.Join(s.dir, "cursors"))
	if err := os.Mkdir(filepath.Join(s.dir, "cursors"), 0700); err != nil {
		t.Fatal(err)
	}
	s.Flush()
	if _, err := os.Stat(s.path("cursors", "c")); err != nil {
		t.Fatalf("failed write was not retried: %v", err)
	}
	s.mu.Lock()
	left := len(s.failed)
	s.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d failed ops left after recovery", left)
	}
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.running
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStoreFailedDeleteIsRetried(t *testing.T) {
	s := newTestStore(t)
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s.onError = func(string, string, error) {}
	p := s.path("cursors", "c")
	if err := os.MkdirAll(filepath.Join(p, "x"), 0700); err != nil {
		t.Fatal(err)
	}
	s.Delete("cursors", "c")
	s.Flush()
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("delete should have failed: %v", err)
	}
	os.Remove(filepath.Join(p, "x"))
	s.Flush()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("failed delete was not retried: %v", err)
	}
}

func TestStoreNewerOpReplacesFailedOne(t *testing.T) {
	s := newTestStore(t)
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s.onError = func(string, string, error) {}
	p := s.path("cursors", "c")
	if err := os.MkdirAll(filepath.Join(p, "x"), 0700); err != nil {
		t.Fatal(err)
	}
	s.Delete("cursors", "c")
	s.Flush()
	os.RemoveAll(p)
	s.Put("cursors", "c", storeItem{ID: "c", N: 7})
	s.Flush()
	time.Sleep(300 * time.Millisecond)
	s.Flush()
	got, _ := LoadAll[storeItem](s, "cursors")
	if len(got) != 1 || got[0].N != 7 {
		t.Fatalf("stale delete was retried over a newer put: %+v", got)
	}
}

func TestStoreRetryBacksOff(t *testing.T) {
	s := newTestStore(t)
	p := s.path("cursors", "c")
	if err := os.MkdirAll(filepath.Join(p, "x"), 0700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	n := 0
	s.onError = func(string, string, error) {
		mu.Lock()
		n++
		mu.Unlock()
	}
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s.Delete("cursors", "c")
	time.Sleep(time.Second)
	mu.Lock()
	got := n
	mu.Unlock()
	if got < 2 || got > 6 {
		t.Fatalf("%d attempts in a second", got)
	}
	os.Remove(filepath.Join(p, "x"))
	s.Flush()
}

func TestStoreFlushIsBounded(t *testing.T) {
	s := newTestStore(t)
	s.flushMax = 200 * time.Millisecond
	tmp := s.path("cursors", "c") + ".tmp"
	if err := unix.Mkfifo(tmp, 0600); err != nil {
		t.Fatal(err)
	}
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s.onError = func(string, string, error) {}
	s.Put("cursors", "c", storeItem{ID: "c"})
	start := time.Now()
	s.Flush()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("flush took %v with a blocked write", d)
	}
	r, err := os.OpenFile(tmp, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	os.Remove(tmp)
	s.flushMax = 5 * time.Second
	waitFor(t, func() bool {
		s.Flush()
		_, err := os.Stat(s.path("cursors", "c"))
		return err == nil
	})
}
