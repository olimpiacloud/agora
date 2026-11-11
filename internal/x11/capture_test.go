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

	"golang.org/x/sys/unix"
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

func TestXUserXvfbAttachesShm(t *testing.T) {
	requireXvfb(t)
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	apps, err := user.Lookup("agora")
	if err != nil {
		t.Skip("apps user agora missing")
	}
	xu, err := user.Lookup("agora-x")
	if err != nil {
		t.Skip("X user agora-x missing")
	}
	uid, _ := strconv.Atoi(apps.Uid)
	gid, _ := strconv.Atoi(apps.Gid)
	xuid, _ := strconv.Atoi(xu.Uid)
	xgid, _ := strconv.Atoi(xu.Gid)
	dir, err := os.MkdirTemp("/tmp", "agora-x11-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	os.Chmod(dir, 0711)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: dir, AuthUid: uid, AuthGid: gid, XUid: xuid, XGid: xgid})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", d.Pid()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(st), fmt.Sprintf("\nUid:\t%d\t%d\t%d\t%d\n", xuid, xuid, xuid, xuid)) {
		t.Fatalf("Xvfb does not run as the X user:\n%s", st)
	}
	var fs unix.Stat_t
	if err := unix.Stat(d.AuthFile, &fs); err != nil {
		t.Fatal(err)
	}
	if int(fs.Uid) != xuid || int(fs.Gid) != gid || fs.Mode&0777 != 0640 {
		t.Fatalf("xauth file %d:%d %o, want %d:%d 640", fs.Uid, fs.Gid, fs.Mode&0777, xuid, gid)
	}
	if err := unix.Lstat(d.Socket(), &fs); err != nil {
		t.Fatal(err)
	}
	if int(fs.Uid) != xuid {
		t.Fatalf("X socket owned by uid %d, want %d", fs.Uid, xuid)
	}
	cp, err := d.NewCapturer()
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if !cp.useShm {
		t.Fatal("X server running as the X user could not attach the MIT-SHM segment")
	}
	var ds unix.SysvShmDesc
	if _, err := unix.SysvShmCtl(cp.shmid, unix.IPC_STAT, &ds); err != nil {
		t.Fatal(err)
	}
	if int(ds.Perm.Uid) != xuid || ds.Perm.Mode&0777 != 0600 {
		t.Fatalf("MIT-SHM segment owned by uid %d mode %o, want uid %d mode 600", ds.Perm.Uid, ds.Perm.Mode&0777, xuid)
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

func TestDialRefusesForeignSocket(t *testing.T) {
	requireXvfb(t)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	other := &Display{Num: d.Num, cookie: d.cookie, owner: d.owner + 1}
	if c, err := other.dial(); err == nil {
		c.Close()
		t.Fatal("dialed an X socket owned by an unexpected uid")
	}
	c, err := d.dial()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
