package x11

import (
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

const xiExt = "XInputExtension"

const (
	XIMasterPointer  = 1
	XIMasterKeyboard = 2
	XISlavePointer   = 3
	XISlaveKeyboard  = 4
	XIFloatingSlave  = 5
)

type XIDevice struct {
	ID         uint16
	Type       uint16
	Attachment uint16
	Enabled    bool
	Name       string
}

func xiInit(c *xgb.Conn) error {
	r, err := xproto.QueryExtension(c, uint16(len(xiExt)), xiExt).Reply()
	if err != nil {
		return err
	}
	if !r.Present {
		return fmt.Errorf("XInputExtension missing")
	}
	c.ExtLock.Lock()
	c.Extensions[xiExt] = r.MajorOpcode
	c.ExtLock.Unlock()
	maj, min, err := xiQueryVersion(c, 2, 2)
	if err != nil {
		return err
	}
	if maj < 2 || (maj == 2 && min < 2) {
		return fmt.Errorf("XInput %d.%d < 2.2", maj, min)
	}
	return nil
}

func xiHeader(c *xgb.Conn, op byte, size int) []byte {
	buf := make([]byte, size)
	c.ExtLock.RLock()
	buf[0] = c.Extensions[xiExt]
	c.ExtLock.RUnlock()
	buf[1] = op
	xgb.Put16(buf[2:], uint16(size/4))
	return buf
}

func pad4(n int) int { return (n + 3) &^ 3 }

func xiQueryVersion(c *xgb.Conn, maj, min uint16) (uint16, uint16, error) {
	buf := xiHeader(c, 47, 8)
	xgb.Put16(buf[4:], maj)
	xgb.Put16(buf[6:], min)
	ck := c.NewCookie(true, true)
	c.NewRequest(buf, ck)
	r, err := ck.Reply()
	if err != nil {
		return 0, 0, err
	}
	return xgb.Get16(r[8:]), xgb.Get16(r[10:]), nil
}

func xiAddMaster(c *xgb.Conn, name string) error {
	nl := len(name)
	clen := 8 + pad4(nl)
	buf := xiHeader(c, 43, 8+clen)
	buf[4] = 1
	b := buf[8:]
	xgb.Put16(b[0:], 1)
	xgb.Put16(b[2:], uint16(clen/4))
	xgb.Put16(b[4:], uint16(nl))
	b[6] = 1
	b[7] = 1
	copy(b[8:], name)
	ck := c.NewCookie(true, false)
	c.NewRequest(buf, ck)
	return ck.Check()
}

func xiRemoveMaster(c *xgb.Conn, masterPointer, retPointer, retKeyboard uint16) error {
	buf := xiHeader(c, 43, 8+12)
	buf[4] = 1
	b := buf[8:]
	xgb.Put16(b[0:], 2)
	xgb.Put16(b[2:], 3)
	xgb.Put16(b[4:], masterPointer)
	b[6] = 2
	xgb.Put16(b[8:], retPointer)
	xgb.Put16(b[10:], retKeyboard)
	ck := c.NewCookie(true, false)
	c.NewRequest(buf, ck)
	return ck.Check()
}

func xiSetClientPointer(c *xgb.Conn, win xproto.Window, dev uint16) error {
	buf := xiHeader(c, 44, 12)
	xgb.Put32(buf[4:], uint32(win))
	xgb.Put16(buf[8:], dev)
	ck := c.NewCookie(true, false)
	c.NewRequest(buf, ck)
	return ck.Check()
}

func xiSetFocus(c *xgb.Conn, win xproto.Window, dev uint16) error {
	buf := xiHeader(c, 49, 16)
	xgb.Put32(buf[4:], uint32(win))
	xgb.Put32(buf[8:], 0)
	xgb.Put16(buf[12:], dev)
	ck := c.NewCookie(true, false)
	c.NewRequest(buf, ck)
	return ck.Check()
}

func xiGetFocus(c *xgb.Conn, dev uint16) (xproto.Window, error) {
	buf := xiHeader(c, 50, 8)
	xgb.Put16(buf[4:], dev)
	ck := c.NewCookie(true, true)
	c.NewRequest(buf, ck)
	r, err := ck.Reply()
	if err != nil {
		return 0, err
	}
	return xproto.Window(xgb.Get32(r[8:])), nil
}

func xiWarpPointer(c *xgb.Conn, dst xproto.Window, x, y int, dev uint16) error {
	buf := xiHeader(c, 41, 36)
	xgb.Put32(buf[8:], uint32(dst))
	xgb.Put32(buf[24:], uint32(int32(x)<<16))
	xgb.Put32(buf[28:], uint32(int32(y)<<16))
	xgb.Put16(buf[32:], dev)
	ck := c.NewCookie(true, false)
	c.NewRequest(buf, ck)
	return ck.Check()
}

func xiQueryPointer(c *xgb.Conn, win xproto.Window, dev uint16) (x, y int, child xproto.Window, err error) {
	buf := xiHeader(c, 40, 12)
	xgb.Put32(buf[4:], uint32(win))
	xgb.Put16(buf[8:], dev)
	ck := c.NewCookie(true, true)
	c.NewRequest(buf, ck)
	r, e := ck.Reply()
	if e != nil {
		return 0, 0, 0, e
	}
	child = xproto.Window(xgb.Get32(r[12:]))
	x = int(int32(xgb.Get32(r[16:])) >> 16)
	y = int(int32(xgb.Get32(r[20:])) >> 16)
	return x, y, child, nil
}

func xiQueryDevices(c *xgb.Conn) ([]XIDevice, error) {
	buf := xiHeader(c, 48, 8)
	xgb.Put16(buf[4:], 0)
	ck := c.NewCookie(true, true)
	c.NewRequest(buf, ck)
	r, err := ck.Reply()
	if err != nil {
		return nil, err
	}
	n := int(xgb.Get16(r[8:]))
	off := 32
	out := make([]XIDevice, 0, n)
	for i := 0; i < n && off+12 <= len(r); i++ {
		d := XIDevice{
			ID:         xgb.Get16(r[off:]),
			Type:       xgb.Get16(r[off+2:]),
			Attachment: xgb.Get16(r[off+4:]),
			Enabled:    r[off+10] != 0,
		}
		nc := int(xgb.Get16(r[off+6:]))
		nl := int(xgb.Get16(r[off+8:]))
		d.Name = string(r[off+12 : off+12+nl])
		off += 12 + pad4(nl)
		for j := 0; j < nc && off+4 <= len(r); j++ {
			l := int(xgb.Get16(r[off+2:])) * 4
			off += l
		}
		out = append(out, d)
	}
	return out, nil
}
