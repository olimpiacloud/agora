package stream

import (
	"errors"
	"testing"
	"time"
)

func fakeClock(t *testing.T) func(time.Duration) {
	now := time.Unix(1000, 0)
	orig := sinkNow
	sinkNow = func() time.Time { return now }
	t.Cleanup(func() { sinkNow = orig })
	return func(d time.Duration) { now = now.Add(d) }
}

func TestQueueSinkKeyBackoff(t *testing.T) {
	advance := fakeClock(t)
	block := make(chan struct{})
	defer close(block)
	s := NewQueueSink("q", Interactive, 1, func(*Frame) error { <-block; return nil })
	defer s.Close()
	key := &Frame{Key: true}
	delta := &Frame{}
	if !s.Push(key) {
		t.Fatal("first keyframe refused")
	}
	for i := 0; i < 100 && len(s.ch) > 0; i++ {
		time.Sleep(time.Millisecond)
	}
	if !s.Push(delta) {
		t.Fatal("delta refused with room in the queue")
	}
	if s.Push(delta) {
		t.Fatal("first overflow must ask for a keyframe")
	}
	asks := 0
	var gaps []time.Duration
	var last time.Duration
	for el := time.Duration(0); el <= 40*time.Second; el += 100 * time.Millisecond {
		if !s.Push(delta) {
			asks++
			gaps = append(gaps, el-last)
			last = el
		}
		advance(100 * time.Millisecond)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	if len(gaps) < len(want) {
		t.Fatalf("gaps %v", gaps)
	}
	for i, w := range want {
		if gaps[i] < w || gaps[i] > w+200*time.Millisecond {
			t.Fatalf("gap %d = %v, want %v (all %v)", i, gaps[i], w, gaps)
		}
	}
	if asks > 8 {
		t.Fatalf("%d keyframe requests in 40s", asks)
	}
}

func TestQueueSinkBackoffResetsAfterHealthyKeyframe(t *testing.T) {
	advance := fakeClock(t)
	release := make(chan struct{}, 64)
	s := NewQueueSink("q", Interactive, 1, func(*Frame) error { <-release; return nil })
	defer s.Close()
	key := &Frame{Key: true}
	delta := &Frame{}
	s.Push(key)
	for i := 0; i < 100 && len(s.ch) > 0; i++ {
		time.Sleep(time.Millisecond)
	}
	s.Push(delta)
	if s.Push(delta) {
		t.Fatal("overflow must ask for a keyframe")
	}
	advance(time.Second)
	if s.Push(delta) {
		t.Fatal("second request after 1s")
	}
	advance(500 * time.Millisecond)
	if !s.Push(delta) {
		t.Fatal("request inside the 2s backoff")
	}
	release <- struct{}{}
	release <- struct{}{}
	for i := 0; i < 100 && len(s.ch) > 0; i++ {
		time.Sleep(time.Millisecond)
	}
	advance(2 * time.Second)
	if !s.Push(key) {
		t.Fatal("keyframe refused with room in the queue")
	}
	for i := 0; i < 100 && len(s.ch) > 0; i++ {
		time.Sleep(time.Millisecond)
	}
	advance(5 * time.Second)
	if !s.Push(delta) {
		t.Fatal("delta refused")
	}
	if s.Push(delta) {
		t.Fatal("overflow after a healthy stretch must ask right away")
	}
	advance(time.Second)
	if s.Push(delta) {
		t.Fatal("backoff should be back to 1s")
	}
	close(release)
}

func TestQueueSinkDeadAfterWriteError(t *testing.T) {
	errc := make(chan error, 1)
	s := NewQueueSink("q", Interactive, 4, func(*Frame) error { return errors.New("gone") })
	s.OnError = func(err error) { errc <- err }
	defer s.Close()
	if !s.Push(&Frame{Key: true}) {
		t.Fatal("keyframe refused")
	}
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("OnError not called")
	}
	if !s.Dead() {
		t.Fatal("sink not dead after a write error")
	}
	for i := 0; i < 20; i++ {
		if !s.Push(&Frame{}) {
			t.Fatal("dead sink must not ask for keyframes")
		}
	}
	e := &Engine{sinks: map[string]Sink{"q": s, "t": &countSink{}}}
	got, _ := e.snapshotSinks()
	if len(got) != 1 || got[0].ID() != "t" {
		t.Fatalf("snapshot kept the dead sink: %d sinks", len(got))
	}
}
