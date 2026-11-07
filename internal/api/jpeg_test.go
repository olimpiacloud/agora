package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestJPEGProducerShared(t *testing.T) {
	var runs atomic.Int32
	stopped := make(chan struct{})
	pubc := make(chan func(bool, []byte), 1)
	run := func(ctx context.Context, pub func(bool, []byte)) {
		runs.Add(1)
		pubc <- pub
		<-ctx.Done()
		close(stopped)
	}
	key := jpegKey{cursor: "c", fps: 5, maxW: 640, quality: 60}
	a, err := jpegSubscribe("w1", key, run)
	if err != nil {
		t.Fatal(err)
	}
	b, err := jpegSubscribe("w1", key, run)
	if err != nil {
		t.Fatal(err)
	}
	pub := <-pubc
	pub(true, []byte("region"))
	pub(false, []byte("frame1"))
	pub(false, []byte("frame2"))
	for _, s := range []*jpegSub{a, b} {
		select {
		case <-s.notify:
		case <-time.After(time.Second):
			t.Fatal("no notification")
		}
		r, rs, f, fs, dead := s.p.snapshot()
		if string(r) != "region" || rs != 1 || string(f) != "frame2" || fs != 2 || dead {
			t.Fatalf("snapshot %q %d %q %d %v", r, rs, f, fs, dead)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("producer started %d times", runs.Load())
	}
	a.close()
	a.close()
	select {
	case <-stopped:
		t.Fatal("producer stopped while a viewer remains")
	case <-time.After(50 * time.Millisecond):
	}
	b.close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("producer still running without viewers")
	}
	if len(jpegHub.prods) != 0 || jpegHub.views["w1"] != 0 {
		t.Fatalf("hub not empty: %v %v", jpegHub.prods, jpegHub.views)
	}
}

func TestJPEGViewerCap(t *testing.T) {
	run := func(ctx context.Context, pub func(bool, []byte)) { <-ctx.Done() }
	var subs []*jpegSub
	for i := 0; i < maxJPEGViewers; i++ {
		s, err := jpegSubscribe("w2", jpegKey{win: uint64(i)}, run)
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}
	if _, err := jpegSubscribe("w2", jpegKey{}, run); err == nil {
		t.Fatal("cap not enforced")
	}
	if s, err := jpegSubscribe("w3", jpegKey{}, run); err != nil {
		t.Fatal("cap must be per workspace")
	} else {
		s.close()
	}
	for _, s := range subs {
		s.close()
	}
	if s, err := jpegSubscribe("w2", jpegKey{}, run); err != nil {
		t.Fatal(err)
	} else {
		s.close()
	}
}

func TestJPEGProducerExitEndsViewers(t *testing.T) {
	s, err := jpegSubscribe("w4", jpegKey{fps: 1}, func(ctx context.Context, pub func(bool, []byte)) {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	deadline := time.After(time.Second)
	for {
		select {
		case <-s.notify:
		case <-deadline:
			t.Fatal("viewer not told the producer died")
		}
		if _, _, _, _, dead := s.p.snapshot(); dead {
			return
		}
	}
}
