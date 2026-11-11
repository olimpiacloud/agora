package x11

import (
	"fmt"
	"image"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/damage"
	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/sys/unix"
)

type Frame struct {
	X      int
	Y      int
	Width  int
	Height int
	Stride int
	Pix    []byte
}

func (f *Frame) RGBA() *image.RGBA {
	return f.RGBAInto(nil)
}

func (f *Frame) RGBAInto(img *image.RGBA) *image.RGBA {
	if img == nil || img.Rect != image.Rect(0, 0, f.Width, f.Height) {
		img = image.NewRGBA(image.Rect(0, 0, f.Width, f.Height))
	}
	for y := 0; y < f.Height; y++ {
		src := f.Pix[y*f.Stride : y*f.Stride+f.Width*4]
		dst := img.Pix[y*img.Stride : y*img.Stride+f.Width*4]
		for x := 0; x < f.Width*4; x += 4 {
			dst[x] = src[x+2]
			dst[x+1] = src[x+1]
			dst[x+2] = src[x]
			dst[x+3] = 255
		}
	}
	return img
}

type Capturer struct {
	d        *Display
	c        *xgb.Conn
	mu       sync.Mutex
	seg      shm.Seg
	shmid    int
	buf      []byte
	useShm   bool
	redirect bool
}

func (d *Display) NewCapturer() (*Capturer, error) {
	c, err := d.Dial()
	if err != nil {
		return nil, err
	}
	cp := &Capturer{d: d, c: c}
	if err := extInit("MIT-SHM", c); err == nil {
		size := 3840 * 2160 * 4
		id, err := unix.SysvShmGet(unix.IPC_PRIVATE, size, unix.IPC_CREAT|0600)
		if err == nil {
			b, err := unix.SysvShmAttach(id, 0, 0)
			if err == nil {
				seg, _ := c.NewId()
				if err := shm.AttachChecked(c, shm.Seg(seg), uint32(id), false).Check(); err == nil {
					cp.seg = shm.Seg(seg)
					cp.shmid = id
					cp.buf = b
					cp.useShm = true
				} else {
					unix.SysvShmDetach(b)
				}
			}
			unix.SysvShmCtl(id, unix.IPC_RMID, nil)
		}
	}
	return cp, nil
}

func (cp *Capturer) Close() {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.useShm {
		shm.Detach(cp.c, cp.seg)
		xproto.GetInputFocus(cp.c).Reply()
		unix.SysvShmDetach(cp.buf)
		cp.useShm = false
	}
	cp.c.Close()
}

func (cp *Capturer) grab(dr xproto.Drawable, x, y, w, h int, dst *Frame) (*Frame, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("empty capture")
	}
	if dst == nil || cap(dst.Pix) < w*h*4 {
		dst = &Frame{Pix: make([]byte, w*h*4)}
	}
	dst.Width, dst.Height, dst.Stride = w, h, w*4
	dst.Pix = dst.Pix[:w*h*4]
	if cp.useShm && w*h*4 <= len(cp.buf) {
		_, err := shm.GetImage(cp.c, dr, int16(x), int16(y), uint16(w), uint16(h), 0xffffffff, xproto.ImageFormatZPixmap, cp.seg, 0).Reply()
		if err != nil {
			return nil, err
		}
		copy(dst.Pix, cp.buf[:w*h*4])
		return dst, nil
	}
	rows := (262144 - 64) / (w * 4)
	if rows < 1 {
		rows = 1
	}
	for y0 := 0; y0 < h; y0 += rows {
		n := rows
		if y0+n > h {
			n = h - y0
		}
		r, err := xproto.GetImage(cp.c, xproto.ImageFormatZPixmap, dr, int16(x), int16(y+y0), uint16(w), uint16(n), 0xffffffff).Reply()
		if err != nil {
			return nil, err
		}
		copy(dst.Pix[y0*w*4:], r.Data)
	}
	return dst, nil
}

func (cp *Capturer) Grab(x, y, w, h int, dst *Frame) (*Frame, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	sw, sh := cp.d.Size()
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > sw {
		w = sw - x
	}
	if y+h > sh {
		h = sh - y
	}
	f, err := cp.grab(xproto.Drawable(cp.d.root), x, y, w, h, dst)
	if f != nil {
		f.X, f.Y = x, y
	}
	return f, err
}

func (cp *Capturer) GrabScreen(dst *Frame) (*Frame, error) {
	w, h := cp.d.Size()
	return cp.Grab(0, 0, w, h, dst)
}

func (cp *Capturer) GrabWindow(id uint32, dst *Frame) (*Frame, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	w := xproto.Window(id)
	if !cp.redirect {
		if err := extInit("Composite", cp.c); err != nil {
			return nil, err
		}
		composite.QueryVersion(cp.c, 0, 4).Reply()
		if err := composite.RedirectSubwindowsChecked(cp.c, cp.d.root, composite.RedirectAutomatic).Check(); err != nil {
			return nil, err
		}
		cp.redirect = true
	}
	g, err := xproto.GetGeometry(cp.c, xproto.Drawable(w)).Reply()
	if err != nil {
		return nil, err
	}
	pid, err := cp.c.NewId()
	if err != nil {
		return nil, err
	}
	ox, oy := 0, 0
	if tr, err := xproto.TranslateCoordinates(cp.c, w, cp.d.root, 0, 0).Reply(); err == nil {
		ox, oy = int(tr.DstX), int(tr.DstY)
	}
	pm := xproto.Pixmap(pid)
	var f *Frame
	if err := composite.NameWindowPixmapChecked(cp.c, w, pm).Check(); err != nil {
		f, err = cp.grab(xproto.Drawable(w), 0, 0, int(g.Width), int(g.Height), dst)
		if f != nil {
			f.X, f.Y = ox, oy
		}
		return f, err
	}
	defer xproto.FreePixmap(cp.c, pm)
	f, err = cp.grab(xproto.Drawable(pm), 0, 0, int(g.Width), int(g.Height), dst)
	if f != nil {
		f.X, f.Y = ox, oy
	}
	return f, err
}

type DamageWatcher struct {
	c      *xgb.Conn
	C      chan struct{}
	dmg    damage.Damage
	region xfixes.Region
	done   chan struct{}
	once   sync.Once
}

func (d *Display) WatchDamage() (*DamageWatcher, error) {
	c, err := d.Dial()
	if err != nil {
		return nil, err
	}
	if err := extInit("DAMAGE", c); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := damage.QueryVersion(c, 1, 1).Reply(); err != nil {
		c.Close()
		return nil, err
	}
	if err := extInit("XFIXES", c); err != nil {
		c.Close()
		return nil, err
	}
	xfixes.QueryVersion(c, 5, 0).Reply()
	id, _ := c.NewId()
	rid, _ := c.NewId()
	dw := &DamageWatcher{c: c, C: make(chan struct{}, 1), dmg: damage.Damage(id), region: xfixes.Region(rid), done: make(chan struct{})}
	if err := xfixes.CreateRegionChecked(c, dw.region, nil).Check(); err != nil {
		c.Close()
		return nil, err
	}
	if err := damage.CreateChecked(c, dw.dmg, xproto.Drawable(d.root), damage.ReportLevelNonEmpty).Check(); err != nil {
		c.Close()
		return nil, err
	}
	go dw.loop()
	dw.signal()
	return dw, nil
}

func (dw *DamageWatcher) signal() {
	select {
	case dw.C <- struct{}{}:
	default:
	}
}

func (dw *DamageWatcher) loop() {
	for {
		ev, err := dw.c.WaitForEvent()
		if ev == nil && err == nil {
			return
		}
		if _, ok := ev.(damage.NotifyEvent); ok {
			dw.signal()
		}
	}
}

func (dw *DamageWatcher) Ack() {
	damage.Subtract(dw.c, dw.dmg, 0, 0)
}

func (dw *DamageWatcher) AckRegion() (x, y, w, h int, ok bool) {
	damage.Subtract(dw.c, dw.dmg, 0, dw.region)
	r, err := xfixes.FetchRegion(dw.c, dw.region).Reply()
	if err != nil || r.Extents.Width == 0 || r.Extents.Height == 0 {
		return 0, 0, 0, 0, false
	}
	return int(r.Extents.X), int(r.Extents.Y), int(r.Extents.Width), int(r.Extents.Height), true
}

func (dw *DamageWatcher) Close() {
	dw.once.Do(func() {
		damage.Destroy(dw.c, dw.dmg)
		xproto.GetInputFocus(dw.c).Reply()
		dw.c.Close()
	})
}
