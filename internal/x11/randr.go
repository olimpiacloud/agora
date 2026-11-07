package x11

import (
	"fmt"

	"github.com/jezek/xgb/randr"
)

func (d *Display) SetSize(w, h int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.setSize(w, h)
}

func (d *Display) setSize(w, h int) error {
	c := d.conn
	if err := extInit(randr.Init, c); err != nil {
		return err
	}
	if _, err := randr.QueryVersion(c, 1, 5).Reply(); err != nil {
		return err
	}
	res, err := randr.GetScreenResourcesCurrent(c, d.root).Reply()
	if err != nil {
		return err
	}
	if len(res.Crtcs) == 0 || len(res.Outputs) == 0 {
		return fmt.Errorf("no crtc")
	}
	crtc := res.Crtcs[0]
	out := res.Outputs[0]
	ci, err := randr.GetCrtcInfo(c, crtc, res.ConfigTimestamp).Reply()
	if err != nil {
		return err
	}
	if int(ci.Width) == w && int(ci.Height) == h {
		d.Width, d.Height = w, h
		return nil
	}
	name := fmt.Sprintf("%dx%d", w, h)
	var mode randr.Mode
	for i, m := range res.Modes {
		if int(m.Width) == w && int(m.Height) == h {
			mode = randr.Mode(res.Modes[i].Id)
		}
	}
	if mode == 0 {
		mi := randr.ModeInfo{Width: uint16(w), Height: uint16(h), DotClock: uint32(w * h * 60), Htotal: uint16(w), Vtotal: uint16(h), HsyncStart: uint16(w), HsyncEnd: uint16(w), VsyncStart: uint16(h), VsyncEnd: uint16(h), NameLen: uint16(len(name))}
		r, err := randr.CreateMode(c, d.root, mi, name).Reply()
		if err != nil {
			return fmt.Errorf("create mode: %w", err)
		}
		mode = r.Mode
	}
	if err := randr.AddOutputModeChecked(c, out, mode).Check(); err != nil {
		return fmt.Errorf("add mode: %w", err)
	}
	mmw := uint32(float64(w) * 25.4 / 96)
	mmh := uint32(float64(h) * 25.4 / 96)
	grow := w > int(ci.Width) || h > int(ci.Height)
	if grow {
		if err := randr.SetScreenSizeChecked(c, d.root, uint16(max(w, int(ci.Width))), uint16(max(h, int(ci.Height))), mmw, mmh).Check(); err != nil {
			return fmt.Errorf("screen size: %w", err)
		}
	}
	if _, err := randr.SetCrtcConfig(c, crtc, 0, res.ConfigTimestamp, 0, 0, mode, randr.RotationRotate0, []randr.Output{out}).Reply(); err != nil {
		return fmt.Errorf("crtc: %w", err)
	}
	if err := randr.SetScreenSizeChecked(c, d.root, uint16(w), uint16(h), mmw, mmh).Check(); err != nil {
		return fmt.Errorf("screen size: %w", err)
	}
	d.Width, d.Height = w, h
	return nil
}
