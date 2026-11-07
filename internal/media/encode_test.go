package media

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/x11"
)

func TestCaptureEncode(t *testing.T) {
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 1280, Height: 800, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	c := exec.Command("xclock", "-update", "1", "-geometry", "400x400+100+100")
	c.Env = append(os.Environ(), d.Env()...)
	if err := c.Start(); err == nil {
		defer func() { c.Process.Kill(); c.Wait() }()
	}
	time.Sleep(500 * time.Millisecond)
	cp, err := d.NewCapturer()
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	enc, err := NewH264(H264Options{Width: 1280, Height: 800})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	var f *x11.Frame
	n := 60
	var tg, te time.Duration
	total := 0
	for i := 0; i < n; i++ {
		t0 := time.Now()
		f, err = cp.GrabScreen(f)
		if err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		b, _, err := enc.Encode(f.Pix, f.Stride, f.Width, f.Height, int64(i*33), i == 0)
		if err != nil {
			t.Fatal(err)
		}
		tg += t1.Sub(t0)
		te += time.Since(t1)
		total += len(b)
	}
	t.Logf("grab avg %v, convert+encode avg %v, bytes/frame %d", tg/time.Duration(n), te/time.Duration(n), total/n)
	w, err := d.Windows()
	t.Logf("windows %+v %v", w, err)
	if len(w) > 0 {
		wf, err := cp.GrabWindow(w[0].ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("window grab %dx%d", wf.Width, wf.Height)
	}
}
