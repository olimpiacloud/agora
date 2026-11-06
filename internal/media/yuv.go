package media

import (
	"runtime"
	"sync"
)

func BGRXToI420(src []byte, stride, sw, sh int, y []byte, ys int, u, v []byte, uvs int, w, h int) {
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
					ur[cx] = byte(((-38*rr - 74*gg + 112*bb + 128) >> 8) + 128)
					vr[cx] = byte(((112*rr - 94*gg - 18*bb + 128) >> 8) + 128)
				}
			}
		}(r0, r1)
	}
	wg.Wait()
}
