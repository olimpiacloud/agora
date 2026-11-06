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
	"github.com/jezek/xgb/xproto"
)

type DisplayOptions struct {
	Width   int
	Height  int
	Depth   int
	RunDir  string
	MaxWidth  int
	MaxHeight int
	MinNum    int
	Xvfb      string
}

type Display struct {
	Num      int
	Width    int
	Height   int
	AuthFile string
	cookie   string
	proc     *exec.Cmd
	conn     *xgb.Conn
	root     xproto.Window
	screen   *xproto.ScreenInfo
	mu       sync.Mutex
	exited   chan struct{}
	exitErr  error
	keymap   *keymap
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
		f, err := os.OpenFile(lock+".carbon", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			continue
		}
		return n, f, nil
	}
	return 0, nil, errors.New("no free X display")
}

func writeXauth(path string, num int, cookie []byte) error {
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
	return os.WriteFile(path, b.Bytes(), 0600)
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
	if err := os.MkdirAll(o.RunDir, 0700); err != nil {
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
	if err := writeXauth(auth, num, raw); err != nil {
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
	logf, _ := os.Create(filepath.Join(o.RunDir, fmt.Sprintf("xvfb-%d.log", num)))
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	d := &Display{Num: num, Width: o.Width, Height: o.Height, AuthFile: auth, cookie: hex.EncodeToString(raw), proc: cmd, exited: make(chan struct{})}
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
	nc, err := net.DialTimeout("unix", socketPath(d.Num), time.Second)
	if err != nil {
		return nil, err
	}
	c, err := xgb.NewConnNetWithCookieHex(nc, d.cookie)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

func (d *Display) Name() string { return fmt.Sprintf(":%d", d.Num) }

func (d *Display) Env() []string {
	return []string{"DISPLAY=" + d.Name(), "XAUTHORITY=" + d.AuthFile}
}

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
