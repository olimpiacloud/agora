package x11

import (
	"fmt"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

type Seat struct {
	Name     string
	Pointer  uint16
	Keyboard uint16
	owned    bool
	d        *Display
	c        *xgb.Conn
	mu       sync.Mutex
	held     map[uint32]bool
	x, y     int
}

func (d *Display) devices() ([]XIDevice, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return xiQueryDevices(d.conn)
}

func (d *Display) CoreSeat() (*Seat, error) {
	return d.openSeat("core", 2, 3, false)
}

func (d *Display) NewSeat(name string) (*Seat, error) {
	d.mu.Lock()
	err := xiAddMaster(d.conn, name)
	d.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("add master: %w", err)
	}
	devs, err := d.devices()
	if err != nil {
		return nil, err
	}
	var ptr, kbd uint16
	for _, dv := range devs {
		if dv.Type == XIMasterPointer && dv.Name == name+" pointer" {
			ptr = dv.ID
			kbd = dv.Attachment
		}
	}
	if ptr == 0 {
		return nil, fmt.Errorf("master %q not found", name)
	}
	st, err := d.openSeat(name, ptr, kbd, true)
	if err != nil {
		d.mu.Lock()
		xiRemoveMaster(d.conn, ptr, 2, 3)
		d.mu.Unlock()
	}
	return st, err
}

func (d *Display) openSeat(name string, ptr, kbd uint16, owned bool) (*Seat, error) {
	c, err := d.Dial()
	if err != nil {
		return nil, err
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		return nil, err
	}
	if err := xiInit(c); err != nil {
		c.Close()
		return nil, err
	}
	if err := xiSetClientPointer(c, 0, ptr); err != nil {
		c.Close()
		return nil, err
	}
	s := &Seat{Name: name, Pointer: ptr, Keyboard: kbd, owned: owned, d: d, c: c, held: map[uint32]bool{}}
	s.x, s.y, _, _ = xiQueryPointer(c, d.root, ptr)
	return s, nil
}

func (s *Seat) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sym := range s.held {
		s.keyLocked(sym, false)
	}
	for b := byte(1); b <= 3; b++ {
		xtest.FakeInput(s.c, 5, b, 0, s.d.root, 0, 0, 0)
	}
	s.sync()
	var err error
	if s.owned {
		s.d.mu.Lock()
		err = xiRemoveMaster(s.d.conn, s.Pointer, 2, 3)
		s.d.mu.Unlock()
	}
	s.c.Close()
	return err
}

func (s *Seat) sync() {
	xproto.GetInputFocus(s.c).Reply()
}

func (s *Seat) Position() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.x, s.y
}

func (s *Seat) moveLocked(x, y int) {
	xtest.FakeInput(s.c, 6, 0, 0, s.d.root, int16(x), int16(y), 0)
	s.x, s.y = x, y
}

func (s *Seat) Move(x, y int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moveLocked(x, y)
	s.sync()
	return nil
}

func (s *Seat) Button(b int, down bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := byte(5)
	if down {
		t = 4
	}
	xtest.FakeInput(s.c, t, byte(b), 0, s.d.root, 0, 0, 0)
	s.sync()
	return nil
}

func (s *Seat) Click(x, y, b, count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b == 0 {
		b = 1
	}
	if count == 0 {
		count = 1
	}
	s.moveLocked(x, y)
	for i := 0; i < count; i++ {
		xtest.FakeInput(s.c, 4, byte(b), 0, s.d.root, 0, 0, 0)
		xtest.FakeInput(s.c, 5, byte(b), 0, s.d.root, 0, 0, 0)
	}
	s.sync()
	return nil
}

func (s *Seat) Drag(x0, y0, x1, y1 int, steps int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if steps < 2 {
		steps = 12
	}
	s.moveLocked(x0, y0)
	xtest.FakeInput(s.c, 4, 1, 0, s.d.root, 0, 0, 0)
	s.sync()
	for i := 1; i <= steps; i++ {
		x := x0 + (x1-x0)*i/steps
		y := y0 + (y1-y0)*i/steps
		s.moveLocked(x, y)
		s.sync()
		time.Sleep(8 * time.Millisecond)
	}
	xtest.FakeInput(s.c, 5, 1, 0, s.d.root, 0, 0, 0)
	s.sync()
	return nil
}

func (s *Seat) Scroll(x, y, dx, dy int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moveLocked(x, y)
	press := func(b byte, n int) {
		for i := 0; i < n; i++ {
			xtest.FakeInput(s.c, 4, b, 0, s.d.root, 0, 0, 0)
			xtest.FakeInput(s.c, 5, b, 0, s.d.root, 0, 0, 0)
		}
	}
	if dy > 0 {
		press(5, dy)
	} else if dy < 0 {
		press(4, -dy)
	}
	if dx > 0 {
		press(7, dx)
	} else if dx < 0 {
		press(6, -dx)
	}
	s.sync()
	return nil
}

func (s *Seat) keyLocked(sym uint32, down bool) error {
	e, err := s.d.keymap.lookup(s.c, sym)
	if err != nil {
		return err
	}
	t := byte(3)
	if down {
		t = 2
	}
	if down && e.shift && s.d.keymap.shift != 0 {
		xtest.FakeInput(s.c, 2, byte(s.d.keymap.shift), 0, s.d.root, 0, 0, 0)
	}
	xtest.FakeInput(s.c, t, byte(e.code), 0, s.d.root, 0, 0, 0)
	if !down && e.shift && s.d.keymap.shift != 0 {
		xtest.FakeInput(s.c, 3, byte(s.d.keymap.shift), 0, s.d.root, 0, 0, 0)
	}
	if down {
		s.held[sym] = true
	} else {
		delete(s.held, sym)
	}
	return nil
}

func (s *Seat) Key(sym uint32, down bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.keyLocked(sym, down)
	s.sync()
	return err
}

func (s *Seat) Press(combo string) error {
	syms, err := ParseCombo(combo)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range syms {
		if err := s.keyLocked(k, true); err != nil {
			return err
		}
	}
	for i := len(syms) - 1; i >= 0; i-- {
		s.keyLocked(syms[i], false)
	}
	s.sync()
	return nil
}

func (s *Seat) Type(text string, delay time.Duration) error {
	for _, r := range text {
		sym := RuneKeysym(r)
		s.mu.Lock()
		err := s.keyLocked(sym, true)
		if err == nil {
			s.keyLocked(sym, false)
		}
		s.sync()
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	return nil
}

func (s *Seat) Focus(w xproto.Window) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return xiSetFocus(s.c, w, s.Keyboard)
}

func (s *Seat) Focused() (xproto.Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return xiGetFocus(s.c, s.Keyboard)
}
