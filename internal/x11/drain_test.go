package x11

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func floodThen(t *testing.T, d *Display, c *xgb.Conn, check func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 6000; i++ {
			xproto.DestroyWindow(c, 0x7ffffff0)
		}
		done <- check()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		if pid := d.Pid(); pid > 1 {
			syscall.Kill(-pid, syscall.SIGKILL)
		}
		t.Fatal("X connection stopped answering after 6000 queued errors")
	}
}

func TestConnectionsSurviveQueuedErrors(t *testing.T) {
	requireXvfb(t)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			d.Close()
		}
	})
	t.Run("seat", func(t *testing.T) {
		s, err := d.NewSeat("drain")
		if err != nil {
			t.Fatal(err)
		}
		floodThen(t, d, s.c, func() error { return s.Move(5, 5) })
		s.Close()
	})
	t.Run("main", func(t *testing.T) {
		floodThen(t, d, d.conn, func() error { _, err := d.Windows(); return err })
	})
	t.Run("capturer", func(t *testing.T) {
		cp, err := d.NewCapturer()
		if err != nil {
			t.Fatal(err)
		}
		floodThen(t, d, cp.c, func() error { _, err := cp.GrabScreen(nil); return err })
		cp.Close()
	})
}

func TestSeatRejectsOutOfRangeInput(t *testing.T) {
	requireXvfb(t)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := d.NewSeat("valid")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for name, err := range map[string]error{
		"button 0":   s.Button(0, true),
		"button 10":  s.Button(10, true),
		"click b99":  s.Click(1, 1, 99, 1),
		"click n500": s.Click(1, 1, 1, 500),
		"scroll dy":  s.Scroll(1, 1, 0, 5000),
		"scroll dx":  s.Scroll(1, 1, -5000, 0),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, f := range map[string]func() error{
		"click defaults": func() error { return s.Click(1, 1, 0, 0) },
		"double right":   func() error { return s.Click(1, 1, 3, 2) },
		"back button": func() error {
			if err := s.Button(8, true); err != nil {
				return err
			}
			return s.Button(8, false)
		},
		"scroll 3": func() error { return s.Scroll(1, 1, 0, 3) },
	} {
		if err := f(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPressReleasesKeysWhenAComboFails(t *testing.T) {
	requireXvfb(t)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := d.NewSeat("combo")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d.keymap.mu.Lock()
	d.keymap.scratch = nil
	d.keymap.mu.Unlock()
	if err := s.Press("ctrl+ж"); err == nil {
		t.Fatal("press of a key outside the layout with no spare keycode succeeded")
	}
	s.mu.Lock()
	n := len(s.held)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d keys still held after a failed combo", n)
	}
}
