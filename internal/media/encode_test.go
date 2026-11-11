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
	requireXvfb(t)
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

func requireXvfb(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb missing")
	}
}

func TestH264ReconfigureMaxAndBuffer(t *testing.T) {
	enc, err := NewH264(H264Options{Width: 320, Height: 240, FPS: 30, CRF: 23, MaxKbps: 8000})
	if err != nil {
		t.Fatal(err)
	}
	src := make([]byte, 320*240*4)
	if _, key, err := enc.Encode(src, 320*4, 320, 240, 0, true); err != nil || !key {
		t.Fatalf("first frame key=%v err=%v", key, err)
	}
	if err := enc.Reconfigure(23, 16000, 8000); err != nil {
		t.Fatal(err)
	}
	for i := range src {
		src[i] = byte(i)
	}
	if b, key, err := enc.Encode(src, 320*4, 320, 240, 66, false); err != nil || key || len(b) == 0 {
		t.Fatalf("frame after reconfigure: %d bytes key=%v err=%v", len(b), key, err)
	}
	enc.Close()
	if err := enc.Reconfigure(23, 8000, 8000); err == nil {
		t.Fatal("reconfigure of a closed encoder succeeded")
	}
}
