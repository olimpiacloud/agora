package media

import (
	"errors"
	"runtime"
	"sync"
)

var ErrShortFrame = errors.New("media: frame buffer shorter than its size")

func fits(src []byte, stride, w, h int) bool {
	return w > 0 && h > 0 && stride >= w*4 && len(src) >= (h-1)*stride+w*4
}

func BGRXToI420(src []byte, stride, sw, sh int, y []byte, ys int, u, v []byte, uvs int, w, h int) error {
	if !fits(src, stride, sw, sh) {
		return ErrShortFrame
	}
	bgrxToYUV(src, stride, sw, sh, y, ys, u, v, uvs, 1, w, h)
	return nil
}

func BGRXToNV12(src []byte, stride, sw, sh int, y []byte, ys int, uv []byte, uvs int, w, h int) error {
	if !fits(src, stride, sw, sh) {
		return ErrShortFrame
	}
	bgrxToYUV(src, stride, sw, sh, y, ys, uv, uv[1:], uvs, 2, w, h)
	return nil
}

func bgrxToYUV(src []byte, stride, sw, sh int, y []byte, ys int, u, v []byte, uvs, step int, w, h int) {
	workers := runtime.NumCPU()
	if workers > 4 {
		workers = 4
	}
	rowsPer := ((h/2 + workers - 1) / workers)
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		r0 := k * rowsPer
		r1 := r0 + rowsPer
		if r1 > h/2 {
			r1 = h / 2
		}
		if r0 >= r1 {
			break
		}
		wg.Add(1)
		go func(r0, r1 int) {
			defer wg.Done()
			for cy := r0; cy < r1; cy++ {
				for dy := 0; dy < 2; dy++ {
					py := cy*2 + dy
					sy := py
					if sy >= sh {
						sy = sh - 1
					}
					row := src[sy*stride:]
					yr := y[py*ys:]
					for px := 0; px < w; px++ {
						sx := px
						if sx >= sw {
							sx = sw - 1
						}
						o := sx * 4
						b, g, r := int(row[o]), int(row[o+1]), int(row[o+2])
						yr[px] = byte(((66*r + 129*g + 25*b + 128) >> 8) + 16)
					}
				}
				sy0 := cy * 2
				sy1 := sy0 + 1
				if sy0 >= sh {
					sy0 = sh - 1
				}
				if sy1 >= sh {
					sy1 = sh - 1
				}
				r0w := src[sy0*stride:]
				r1w := src[sy1*stride:]
				ur := u[cy*uvs:]
				vr := v[cy*uvs:]
				for cx := 0; cx < w/2; cx++ {
					sx0 := cx * 2
					sx1 := sx0 + 1
					if sx0 >= sw {
						sx0 = sw - 1
					}
					if sx1 >= sw {
						sx1 = sw - 1
					}
					a, b2, c, d := sx0*4, sx1*4, sx0*4, sx1*4
					bb := int(r0w[a]) + int(r0w[b2]) + int(r1w[c]) + int(r1w[d])
					gg := int(r0w[a+1]) + int(r0w[b2+1]) + int(r1w[c+1]) + int(r1w[d+1])
					rr := int(r0w[a+2]) + int(r0w[b2+2]) + int(r1w[c+2]) + int(r1w[d+2])
					bb, gg, rr = bb>>2, gg>>2, rr>>2
					ur[cx*step] = byte(((-38*rr - 74*gg + 112*bb + 128) >> 8) + 128)
					vr[cx*step] = byte(((112*rr - 94*gg - 18*bb + 128) >> 8) + 128)
				}
			}
		}(r0, r1)
	}
	wg.Wait()
}

func BGRXRectToI420(src []byte, stride int, rx, ry, rw, rh int, y []byte, ys int, u, v []byte, uvs int) error {
	if !fits(src, stride, rw, rh) {
		return ErrShortFrame
	}
	bgrxRectToYUV(src, stride, rx, ry, rw, rh, y, ys, u, v, uvs, 1)
	return nil
}

func BGRXRectToNV12(src []byte, stride int, rx, ry, rw, rh int, y []byte, ys int, uv []byte, uvs int) error {
	if !fits(src, stride, rw, rh) {
		return ErrShortFrame
	}
	bgrxRectToYUV(src, stride, rx, ry, rw, rh, y, ys, uv, uv[1:], uvs, 2)
	return nil
}

func bgrxRectToYUV(src []byte, stride int, rx, ry, rw, rh int, y []byte, ys int, u, v []byte, uvs, step int) {
	workers := runtime.NumCPU()
	if workers > 4 {
		workers = 4
	}
	crows := rh / 2
	if crows < 32 {
		workers = 1
	}
	per := (crows + workers - 1) / workers
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		c0 := k * per
		c1 := c0 + per
		if c1 > crows {
			c1 = crows
		}
		if c0 >= c1 {
			break
		}
		wg.Add(1)
		go func(c0, c1 int) {
			defer wg.Done()
			for cy := c0; cy < c1; cy++ {
				r0 := src[(cy*2)*stride:]
				r1 := src[(cy*2+1)*stride:]
				y0 := y[(ry+cy*2)*ys+rx:]
				y1 := y[(ry+cy*2+1)*ys+rx:]
				ur := u[(ry/2+cy)*uvs+rx/2*step:]
				vr := v[(ry/2+cy)*uvs+rx/2*step:]
				for cx := 0; cx < rw/2; cx++ {
					o := cx * 8
					b00, g00, r00 := int(r0[o]), int(r0[o+1]), int(r0[o+2])
					b01, g01, r01 := int(r0[o+4]), int(r0[o+5]), int(r0[o+6])
					b10, g10, r10 := int(r1[o]), int(r1[o+1]), int(r1[o+2])
					b11, g11, r11 := int(r1[o+4]), int(r1[o+5]), int(r1[o+6])
					y0[cx*2] = byte(((66*r00 + 129*g00 + 25*b00 + 128) >> 8) + 16)
					y0[cx*2+1] = byte(((66*r01 + 129*g01 + 25*b01 + 128) >> 8) + 16)
					y1[cx*2] = byte(((66*r10 + 129*g10 + 25*b10 + 128) >> 8) + 16)
					y1[cx*2+1] = byte(((66*r11 + 129*g11 + 25*b11 + 128) >> 8) + 16)
					bb := (b00 + b01 + b10 + b11) >> 2
					gg := (g00 + g01 + g10 + g11) >> 2
					rr := (r00 + r01 + r10 + r11) >> 2
					ur[cx*step] = byte(((-38*rr - 74*gg + 112*bb + 128) >> 8) + 128)
					vr[cx*step] = byte(((112*rr - 94*gg - 18*bb + 128) >> 8) + 128)
				}
			}
		}(c0, c1)
	}
	wg.Wait()
}
