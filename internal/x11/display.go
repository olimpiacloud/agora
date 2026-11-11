package x11

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/damage"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"github.com/olimpiacloud/agora/internal/safefs"
	"golang.org/x/sys/unix"
)

type DisplayOptions struct {
	Width     int
	Height    int
	Depth     int
	RunDir    string
	MaxWidth  int
	MaxHeight int
	MinNum    int
	Xvfb      string
	AuthUid   int
	AuthGid   int
	XUid      int
	XGid      int
	CgroupFD  int
}

type Display struct {
	Num       int
	Width     int
	Height    int
	MaxWidth  int
	MaxHeight int
	AuthFile  string
	cookie    string
	xuid      int
	xgid      int
	owner     int
	proc      *exec.Cmd
	conn      *xgb.Conn
	root      xproto.Window
	screen    *xproto.ScreenInfo
	mu        sync.Mutex
	sizeMu    sync.Mutex
	randr     bool
	exited    chan struct{}
	exitErr   error
	keymap    *keymap
}

func socketPath(n int) string { return fmt.Sprintf("/tmp/.X11-unix/X%d", n) }

func freeDisplay(min int) (int, *os.File, error) {
	for n := min; n < min+400; n++ {
		lock := fmt.Sprintf("/tmp/.X%d-lock", n)
		if _, err := os.Stat(lock); err == nil {
			continue
		}
		if _, err := os.Stat(socketPath(n)); err == nil {
			continue
		}
		f, err := os.OpenFile(lock+".agora", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			continue
		}
		return n, f, nil
	}
	return 0, nil, errors.New("no free X display")
}

func socketDir() error {
	const dir = "/tmp/.X11-unix"
	if err := os.Mkdir(dir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return os.Chmod(dir, 0777|os.ModeSticky)
}

func writeXauth(path string, num int, cookie []byte, uid, gid int, shared bool) error {
	var b bytes.Buffer
	put := func(v []byte) {
		binary.Write(&b, binary.BigEndian, uint16(len(v)))
		b.Write(v)
	}
	binary.Write(&b, binary.BigEndian, uint16(65535))
	put([]byte{})
	put([]byte(strconv.Itoa(num)))
	put([]byte("MIT-MAGIC-COOKIE-1"))
	put(cookie)
	os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(b.Bytes()); err != nil {
		return err
	}
	if shared {
		if err := f.Chown(uid, gid); err != nil {
			return err
		}
		if err := f.Chmod(0640); err != nil {
			return err
		}
	}
	return f.Close()
}

func StartDisplay(ctx context.Context, o DisplayOptions) (*Display, error) {
	if o.Width == 0 {
		o.Width = 1280
	}
	if o.Height == 0 {
		o.Height = 800
	}
	if o.Depth == 0 {
		o.Depth = 24
	}
	if o.MaxWidth == 0 {
		o.MaxWidth = 1920
	}
	if o.MaxHeight == 0 {
		o.MaxHeight = 1200
	}
	if o.MinNum == 0 {
		o.MinNum = 120
	}
	if o.Xvfb == "" {
		o.Xvfb = "Xvfb"
	}
	if err := os.MkdirAll(o.RunDir, 0711); err != nil {
		return nil, err
	}
	rd, err := safefs.OpenBeneath(o.RunDir, ".", syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	err = rd.Chmod(0711)
	rd.Close()
	if err != nil {
		return nil, err
	}
	num, lockf, err := freeDisplay(o.MinNum)
	if err != nil {
		return nil, err
	}
	defer func() {
		lockf.Close()
		os.Remove(lockf.Name())
	}()
	raw := make([]byte, 16)
	rand.Read(raw)
	auth := filepath.Join(o.RunDir, fmt.Sprintf("xauth-%d", num))
	if err := writeXauth(auth, num, raw, o.XUid, o.AuthGid, o.AuthUid != 0 || o.XUid != 0); err != nil {
		return nil, err
	}
	maxW, maxH := o.MaxWidth, o.MaxHeight
	if maxW < o.Width {
		maxW = o.Width
	}
	if maxH < o.Height {
		maxH = o.Height
	}
	args := []string{
		fmt.Sprintf(":%d", num),
		"-screen", "0", fmt.Sprintf("%dx%dx%d", maxW, maxH, o.Depth),
		"-auth", auth,
		"-nolisten", "tcp",
		"-noreset",
		"-dpi", "96",
		"+extension", "RANDR",
		"+extension", "COMPOSITE",
		"+extension", "DAMAGE",
		"+extension", "XTEST",
	}
	cmd := exec.Command(o.Xvfb, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	owner := os.Geteuid()
	if o.XUid != 0 {
		if err := socketDir(); err != nil {
			return nil, err
		}
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(o.XUid), Gid: uint32(o.XGid)}
		owner = o.XUid
	}
	if o.CgroupFD > 0 {
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = o.CgroupFD
	}
	logf, _ := os.OpenFile(filepath.Join(o.RunDir, fmt.Sprintf("xvfb-%d.log", num)), os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0600)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	d := &Display{Num: num, Width: o.Width, Height: o.Height, MaxWidth: maxW, MaxHeight: maxH, AuthFile: auth, cookie: hex.EncodeToString(raw), xuid: o.XUid, xgid: o.XGid, owner: owner, proc: cmd, exited: make(chan struct{})}
	go func() {
		d.exitErr = cmd.Wait()
		if logf != nil {
			logf.Close()
		}
		close(d.exited)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := d.Dial()
		if err == nil {
			d.conn = c
			break
		}
		select {
		case <-d.exited:
			return nil, fmt.Errorf("Xvfb exited: %v", d.exitErr)
		case <-ctx.Done():
			d.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			d.Close()
			return nil, fmt.Errorf("Xvfb :%d did not start: %v", num, err)
		}
	}
	setup := xproto.Setup(d.conn)
	d.screen = setup.DefaultScreen(d.conn)
	d.root = d.screen.Root
	if err := xiInit(d.conn); err != nil {
		d.Close()
		return nil, err
	}
	if err := d.setSize(o.Width, o.Height); err != nil {
		d.Close()
		return nil, err
	}
	km, err := loadKeymap(d.conn)
	if err != nil {
		d.Close()
		return nil, err
	}
	d.keymap = km
	return d, nil
}

func (d *Display) Dial() (*xgb.Conn, error) {
	if err := d.registerExts(); err != nil {
		return nil, err
	}
	return d.dial()
}

func (d *Display) dial() (*xgb.Conn, error) {
	path := socketPath(d.Num)
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return nil, &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || int(st.Uid) != d.owner {
		return nil, fmt.Errorf("refusing X socket %s: owned by uid %d, want a socket owned by uid %d", path, st.Uid, d.owner)
	}
	nc, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, err
	}
	if err := checkPeer(nc, d.owner); err != nil {
		nc.Close()
		return nil, fmt.Errorf("refusing X socket %s: %w", path, err)
	}
	c, err := xgb.NewConnNetWithCookieHex(nc, d.cookie)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

func checkPeer(nc net.Conn, uid int) error {
	uc, ok := nc.(*net.UnixConn)
	if !ok {
		return errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if int(cred.Uid) != uid {
		return fmt.Errorf("server runs as uid %d, want uid %d", cred.Uid, uid)
	}
	return nil
}

func (d *Display) Name() string { return fmt.Sprintf(":%d", d.Num) }

func (d *Display) Env() []string {
	return []string{"DISPLAY=" + d.Name(), "XAUTHORITY=" + d.AuthFile}
}

func (d *Display) Socket() string { return socketPath(d.Num) }

func (d *Display) Root() xproto.Window { return d.root }

func (d *Display) Conn() *xgb.Conn { return d.conn }

func (d *Display) Pid() int {
	if d.proc != nil && d.proc.Process != nil {
		return d.proc.Process.Pid
	}
	return 0
}

func (d *Display) Exited() <-chan struct{} { return d.exited }

func (d *Display) Close() {
	if d.conn != nil {
		d.conn.Close()
	}
	if d.proc != nil && d.proc.Process != nil {
		syscall.Kill(-d.proc.Process.Pid, syscall.SIGTERM)
		select {
		case <-d.exited:
		case <-time.After(3 * time.Second):
			syscall.Kill(-d.proc.Process.Pid, syscall.SIGKILL)
			<-d.exited
		}
	}
	os.Remove(d.AuthFile)
}

var xExts = []struct {
	name string
	init func(*xgb.Conn) error
}{
	{"MIT-SHM", shm.Init},
	{"Composite", composite.Init},
	{"DAMAGE", damage.Init},
	{"XFIXES", xfixes.Init},
	{"XTEST", xtest.Init},
	{"RANDR", randr.Init},
}

var (
	extMu    sync.Mutex
	extsDone bool
	extReady = map[string]bool{}
)

func (d *Display) registerExts() error {
	extMu.Lock()
	defer extMu.Unlock()
	if extsDone {
		return nil
	}
	c, err := d.dial()
	if err != nil {
		return err
	}
	defer c.Close()
	for _, x := range xExts {
		if x.init(c) == nil {
			extReady[x.name] = true
		}
	}
	extsDone = true
	return nil
}

func extInit(name string, c *xgb.Conn) error {
	extMu.Lock()
	ready := extReady[name]
	extMu.Unlock()
	if !ready {
		return fmt.Errorf("X extension %s is not registered", name)
	}
	r, err := xproto.QueryExtension(c, uint16(len(name)), name).Reply()
	if err != nil {
		return err
	}
	if !r.Present {
		return fmt.Errorf("X extension %s missing", name)
	}
	c.ExtLock.Lock()
	c.Extensions[name] = r.MajorOpcode
	c.ExtLock.Unlock()
	return nil
}
