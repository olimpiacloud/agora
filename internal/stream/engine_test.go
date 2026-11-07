package stream

import (
	"errors"

	"context"
	"github.com/olimpiacloud/agora/internal/media"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/x11"
)

type countSink struct{ n atomic.Int64 }

func (s *countSink) ID() string         { return "t" }
func (s *countSink) Mode() Mode         { return Interactive }
func (s *countSink) Push(f *Frame) bool { s.n.Add(1); return true }
func (s *countSink) Close()             {}

func TestEngineStopsWhenDisplayExits(t *testing.T) {
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(d)
	s := &countSink{}
	e.AddSink(s)
	for i := 0; i < 100 && s.n.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if s.n.Load() == 0 {
		t.Fatal("no frame encoded")
	}
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	d.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("engine loop still running after display exit")
	}
	e.Close()
}

type refuseSink struct{ countSink }

func (s *refuseSink) ID() string         { return "slow" }
func (s *refuseSink) Push(f *Frame) bool { s.n.Add(1); return false }

func TestSlowSinkKeyframesRateLimited(t *testing.T) {
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := NewEngine(d)
	defer e.Close()
	fast := &countSink{}
	e.AddSink(fast)
	e.AddSink(&refuseSink{})
	start := time.Now()
	for time.Since(start) < 3*time.Second {
		e.Damage()
		e.RequestKeyframe()
		time.Sleep(15 * time.Millisecond)
	}
	st := e.Stats()
	if st.Frames < 20 {
		t.Fatalf("only %d frames", st.Frames)
	}
	if st.Keyframes > 6 {
		t.Fatalf("%d keyframes in %d frames over 3s: slow sinks and PLIs must be rate-limited", st.Keyframes, st.Frames)
	}
}

func TestEngineRestartsAfterEncoderError(t *testing.T) {
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var calls atomic.Int32
	orig := newEncoder
	newEncoder = func(o media.H264Options) (media.Encoder, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("injected")
		}
		return orig(o)
	}
	defer func() { newEncoder = orig }()
	e := NewEngine(d)
	defer e.Close()
	s := &countSink{}
	e.AddSink(s)
	for i := 0; i < 200 && s.n.Load() == 0; i++ {
		e.Damage()
		time.Sleep(20 * time.Millisecond)
	}
	if s.n.Load() == 0 {
		t.Fatal("engine did not restart after an encoder error")
	}
	if st := e.Stats(); st.Starts < 2 {
		t.Fatalf("starts = %d", st.Starts)
	}
}
