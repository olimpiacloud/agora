package stream

import (
	"errors"
	"os/exec"

	"context"
	"github.com/olimpiacloud/agora/internal/media"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/olimpiacloud/agora/internal/x11"
)

func painter(t *testing.T, d *x11.Display) func() {
	c, err := d.Dial()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	n := uint32(0)
	return func() {
		n++
		xproto.ChangeWindowAttributes(c, d.Root(), xproto.CwBackPixel, []uint32{n * 0x10203})
		xproto.ClearArea(c, false, d.Root(), 0, 0, 0, 0)
		xproto.GetInputFocus(c).Reply()
	}
}

type countSink struct{ n atomic.Int64 }

func (s *countSink) ID() string         { return "t" }
func (s *countSink) Mode() Mode         { return Interactive }
func (s *countSink) Push(f *Frame) bool { s.n.Add(1); return true }
func (s *countSink) Close()             {}

func TestEngineStopsWhenDisplayExits(t *testing.T) {
	requireXvfb(t)
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
	requireXvfb(t)
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
	paint := painter(t, d)
	start := time.Now()
	for time.Since(start) < 3*time.Second {
		paint()
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
	requireXvfb(t)
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

func TestStaticScreenEncodesNothing(t *testing.T) {
	requireXvfb(t)
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := NewEngine(d)
	defer e.Close()
	s := &countSink{}
	e.AddSink(s)
	for i := 0; i < 100 && s.n.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if s.n.Load() == 0 {
		t.Fatal("no first frame")
	}
	time.Sleep(200 * time.Millisecond)
	base := s.n.Load()
	for i := 0; i < 50; i++ {
		e.Damage()
		time.Sleep(20 * time.Millisecond)
	}
	if got := s.n.Load() - base; got > 1 {
		t.Fatalf("%d frames encoded for a static screen", got)
	}
	base = s.n.Load()
	e.keyReq.Store(true)
	e.poke()
	for i := 0; i < 100 && s.n.Load() == base; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if s.n.Load() == base {
		t.Fatal("forced keyframe produced no frame")
	}
	base = s.n.Load()
	paint := painter(t, d)
	for i := 0; i < 100 && s.n.Load() == base; i++ {
		paint()
		time.Sleep(20 * time.Millisecond)
	}
	if s.n.Load() == base {
		t.Fatal("damage produced no frame")
	}
}

type deadSink struct {
	countSink
	dead atomic.Bool
}

func (s *deadSink) ID() string { return "dead" }
func (s *deadSink) Push(f *Frame) bool {
	s.n.Add(1)
	s.dead.Store(true)
	return false
}
func (s *deadSink) Dead() bool { return s.dead.Load() }

func TestDeadSinkDoesNotForceKeyframes(t *testing.T) {
	requireXvfb(t)
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := NewEngine(d)
	defer e.Close()
	e.AddSink(&countSink{})
	dead := &deadSink{}
	e.AddSink(dead)
	paint := painter(t, d)
	start := time.Now()
	for time.Since(start) < 2500*time.Millisecond {
		paint()
		time.Sleep(15 * time.Millisecond)
	}
	st := e.Stats()
	if dead.n.Load() > 2 {
		t.Fatalf("dead sink got %d frames", dead.n.Load())
	}
	if st.Frames < 20 {
		t.Fatalf("only %d frames", st.Frames)
	}
	if st.Keyframes > 2 {
		t.Fatalf("%d keyframes in %d frames: a dead sink must not force keyframes", st.Keyframes, st.Frames)
	}
}

func TestEngineSurvivesResize(t *testing.T) {
	requireXvfb(t)
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 800, Height: 600, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := NewEngine(d)
	defer e.Close()
	s := &countSink{}
	e.AddSink(s)
	paint := painter(t, d)
	sizes := [][2]int{{800, 600}, {640, 480}, {1024, 700}, {400, 300}}
	start := time.Now()
	for i := 0; time.Since(start) < 2*time.Second; i++ {
		paint()
		if i%5 == 0 {
			sz := sizes[(i/5)%len(sizes)]
			if err := d.SetSize(sz[0], sz[1]); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	base := s.n.Load()
	for i := 0; i < 100 && s.n.Load() == base; i++ {
		paint()
		time.Sleep(20 * time.Millisecond)
	}
	if s.n.Load() == base {
		t.Fatal("engine stopped producing frames after resizes")
	}
	if st := e.Stats(); st.Starts != 1 {
		t.Fatalf("engine restarted %d times", st.Starts-1)
	}
}

func requireXvfb(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb missing")
	}
}
