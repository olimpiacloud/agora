package x11

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"testing"
)

func TestRGBAIntoReuse(t *testing.T) {
	f := &Frame{Width: 5, Height: 3, Stride: 24, Pix: make([]byte, 24*3)}
	for i := range f.Pix {
		f.Pix[i] = byte(i * 11)
	}
	want := f.RGBA()
	img := f.RGBAInto(nil)
	if !bytes.Equal(img.Pix, want.Pix) {
		t.Fatal("RGBAInto differs from RGBA")
	}
	for i := range f.Pix {
		f.Pix[i] = byte(i * 3)
	}
	want = f.RGBA()
	again := f.RGBAInto(img)
	if again != img || !bytes.Equal(again.Pix, want.Pix) {
		t.Fatal("reused RGBAInto differs from RGBA or reallocated")
	}
	f.Width = 4
	if other := f.RGBAInto(img); other == img || other.Rect.Dx() != 4 {
		t.Fatal("new size must get a new image")
	}
}

func TestAppsUserXvfbAttachesShm(t *testing.T) {
	requireXvfb(t)
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	u, err := user.Lookup("agora")
	if err != nil {
		t.Skip("apps user agora missing")
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	dir, err := os.MkdirTemp("/tmp", "agora-x11-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	os.Chmod(dir, 0711)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: dir, AuthUid: uid, AuthGid: gid})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", d.Pid()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(st), fmt.Sprintf("\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)) {
		t.Fatalf("Xvfb does not run as the apps user:\n%s", st)
	}
	cp, err := d.NewCapturer()
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if !cp.useShm {
		t.Fatal("X server running as the apps user could not attach the MIT-SHM segment")
	}
	if err := d.SetSize(800, 600); err != nil {
		t.Fatal(err)
	}
	f, err := cp.GrabScreen(nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Width != 800 || f.Height != 600 {
		t.Fatalf("grab %dx%d after resize, want 800x600", f.Width, f.Height)
	}
}
