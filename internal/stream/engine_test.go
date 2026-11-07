package stream

import (
	"context"
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
