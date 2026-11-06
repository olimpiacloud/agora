package x11

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type keyEntry struct {
	code  xproto.Keycode
	shift bool
}

type keymap struct {
	mu      sync.Mutex
	min     xproto.Keycode
	max     xproto.Keycode
	per     int
	syms    map[uint32]keyEntry
	scratch []xproto.Keycode
	next    int
	shift   xproto.Keycode
}

func loadKeymap(c *xgb.Conn) (*keymap, error) {
	s := xproto.Setup(c)
	min, max := s.MinKeycode, s.MaxKeycode
	r, err := xproto.GetKeyboardMapping(c, min, byte(max-min+1)).Reply()
	if err != nil {
		return nil, err
	}
	k := &keymap{min: min, max: max, per: int(r.KeysymsPerKeycode), syms: map[uint32]keyEntry{}}
	for i := 0; i <= int(max-min); i++ {
		code := min + xproto.Keycode(i)
		row := r.Keysyms[i*k.per : (i+1)*k.per]
		empty := true
		for j, ks := range row {
			if ks == 0 {
				continue
			}
			empty = false
			if j > 1 {
				continue
			}
			if _, ok := k.syms[uint32(ks)]; !ok {
				k.syms[uint32(ks)] = keyEntry{code: code, shift: j == 1}
			}
		}
		if empty && code > 8 {
			k.scratch = append(k.scratch, code)
		}
	}
	if e, ok := k.syms[0xffe1]; ok {
		k.shift = e.code
	}
	return k, nil
}

func (k *keymap) lookup(c *xgb.Conn, sym uint32) (keyEntry, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if e, ok := k.syms[sym]; ok {
		return e, nil
	}
	if len(k.scratch) == 0 {
		return keyEntry{}, fmt.Errorf("no keycode for keysym 0x%x", sym)
	}
	code := k.scratch[k.next%len(k.scratch)]
	k.next++
	for s, e := range k.syms {
		if e.code == code {
			delete(k.syms, s)
		}
	}
	syms := make([]xproto.Keysym, k.per)
	syms[0] = xproto.Keysym(sym)
	if k.per > 1 {
		syms[1] = xproto.Keysym(sym)
	}
	if err := xproto.ChangeKeyboardMappingChecked(c, 1, code, byte(k.per), syms).Check(); err != nil {
		return keyEntry{}, err
	}
	e := keyEntry{code: code}
	k.syms[sym] = e
	return e, nil
}

var namedKeys = map[string]uint32{
	"enter": 0xff0d, "return": 0xff0d, "tab": 0xff09, "escape": 0xff1b, "esc": 0xff1b,
	"backspace": 0xff08, "delete": 0xffff, "del": 0xffff, "insert": 0xff63,
	"home": 0xff50, "end": 0xff57, "pageup": 0xff55, "pagedown": 0xff56,
	"left": 0xff51, "up": 0xff52, "right": 0xff53, "down": 0xff54,
	"arrowleft": 0xff51, "arrowup": 0xff52, "arrowright": 0xff53, "arrowdown": 0xff54,
	"space": 0x20, "shift": 0xffe1, "ctrl": 0xffe3, "control": 0xffe3, "alt": 0xffe9,
	"meta": 0xffeb, "super": 0xffeb, "cmd": 0xffeb, "capslock": 0xffe5, "menu": 0xff67,
	"printscreen": 0xff61,
	"f1":          0xffbe, "f2": 0xffbf, "f3": 0xffc0, "f4": 0xffc1, "f5": 0xffc2, "f6": 0xffc3,
	"f7": 0xffc4, "f8": 0xffc5, "f9": 0xffc6, "f10": 0xffc7, "f11": 0xffc8, "f12": 0xffc9,
}

func RuneKeysym(r rune) uint32 {
	switch r {
	case '\n', '\r':
		return 0xff0d
	case '\t':
		return 0xff09
	case '\b':
		return 0xff08
	}
	if r < 0x100 {
		return uint32(r)
	}
	return 0x01000000 | uint32(r)
}

func ParseKeysym(name string) (uint32, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if v, ok := namedKeys[n]; ok {
		return v, nil
	}
	if utf8.RuneCountInString(name) == 1 {
		r, _ := utf8.DecodeRuneInString(name)
		return RuneKeysym(r), nil
	}
	if len(n) == 4 && strings.HasPrefix(n, "key") {
		return RuneKeysym(rune(n[3])), nil
	}
	if strings.HasPrefix(n, "digit") && len(n) == 6 {
		return uint32(n[5]), nil
	}
	return 0, fmt.Errorf("unknown key %q", name)
}

func ParseCombo(combo string) ([]uint32, error) {
	if combo == "+" {
		return []uint32{'+'}, nil
	}
	parts := strings.Split(combo, "+")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			p = "+"
		}
		s, err := ParseKeysym(p)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
