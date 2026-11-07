package media

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func synth(w, h, t int) []byte {
	b := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 4
			b[o] = byte(x)
			b[o+1] = byte(y)
			b[o+2] = 128
			if x/40 == t%(w/40) && y < 200 {
				b[o], b[o+1], b[o+2] = 255, 255, 255
			}
		}
	}
	return b
}

func decodeFrames(t *testing.T, stream []byte, w, h int) []byte {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found")
	}
	p := filepath.Join(t.TempDir(), "s.h264")
	os.WriteFile(p, stream, 0644)
	out, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", p, "-f", "rawvideo", "-pix_fmt", "gray", "-").Output()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func encodeSeq(t *testing.T, enc Encoder, w, h, n int) []byte {
	var s bytes.Buffer
	for i := 0; i < n; i++ {
		f := synth(w, h, i)
		var b []byte
		var key bool
		var err error
		if i == 0 || i == n/2 {
			b, key, err = enc.Encode(f, w*4, w, h, int64(i*33), true)
			if !key {
				t.Fatalf("frame %d: requested IDR not key", i)
			}
		} else {
			x := (i % (w / 40)) * 40
			px := ((i - 1) % (w / 40)) * 40
			x0, x1 := min(x, px), max(x, px)+40
			if x1-x0 > 120 {
				x0, x1 = 0, w
			}
			sub := make([]byte, (x1-x0)*4*200)
			for y := 0; y < 200; y++ {
				copy(sub[y*(x1-x0)*4:], f[(y*w+x0)*4:(y*w+x1)*4])
			}
			b, key, err = enc.EncodeRect(sub, (x1-x0)*4, x0, 0, x1-x0, 200, int64(i*33), false)
			if key {
				t.Fatalf("frame %d unexpectedly key", i)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		s.Write(b)
	}
	return s.Bytes()
}

func TestVAAPIEncodeDecode(t *testing.T) {
	for _, sz := range [][3]int{{1280, 800, 1}, {1366, 770, 1}, {1280, 800, 0}} {
		w, h := sz[0], sz[1]
		var enc Encoder
		var err error
		if sz[2] == 1 {
			enc, err = NewVAAPI(H264Options{Width: w, Height: h, FPS: 30, CRF: 23, MaxKbps: 8000})
		} else {
			enc, err = NewH264(H264Options{Width: w, Height: h, FPS: 30, CRF: 23, MaxKbps: 8000})
		}
		if err != nil {
			t.Skip(err)
		}
		const n = 60
		stream := encodeSeq(t, enc, w, h, n)
		enc.Close()
		if !bytes.Contains(stream[:64], []byte{0, 0, 1, 0x67}) && !bytes.Contains(stream[:64], []byte{0, 0, 1, 0x27}) {
			var types []int
			for i := 0; i+4 < len(stream) && len(types) < 20; i++ {
				if stream[i] == 0 && stream[i+1] == 0 && stream[i+2] == 1 {
					types = append(types, int(stream[i+3]))
				}
			}
			t.Fatalf("no SPS at start: % x nal bytes %v", stream[:32], types)
		}
		y := decodeFrames(t, stream, w, h)
		if len(y) != n*w*h {
			t.Fatalf("%dx%d: decoded %d bytes, want %d frames", w, h, len(y), n)
		}
		ref := synth(w, h, n-1)
		var diff int
		last := y[(n-1)*w*h:]
		for i := 0; i < w*h; i += 7 {
			o := i * 4
			want := (66*int(ref[o+2])+129*int(ref[o+1])+25*int(ref[o])+128)>>8 + 16
			d := int(last[i]) - want
			if d < 0 {
				d = -d
			}
			diff += d
		}
		avg := float64(diff) / float64(w*h/7)
		t.Logf("%s %dx%d: %d bytes for %d frames, mean luma error %.2f", enc.Name(), w, h, len(stream), n, avg)
		if avg > 8 {
			t.Fatalf("mean luma error too high: %.2f", avg)
		}
	}
}

func TestVAAPIReopen(t *testing.T) {
	for i := 0; i < 50; i++ {
		enc, err := NewVAAPI(H264Options{Width: 640 + i*16, Height: 480})
		if err != nil {
			t.Skip(err)
		}
		f := synth(640+i*16, 480, i)
		if _, _, err := enc.Encode(f, (640+i*16)*4, 640+i*16, 480, 0, true); err != nil {
			t.Fatal(err)
		}
		enc.Close()
	}
}

func TestEncoderFallback(t *testing.T) {
	t.Setenv("AGORA_VAAPI_DEVICE", "/nonexistent")
	t.Setenv("AGORA_ENCODER", "auto")
	vaapiBroken.Store(false)
	defer vaapiBroken.Store(false)
	enc, err := NewEncoder(H264Options{Width: 320, Height: 240})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if enc.Name() != "x264" {
		t.Fatalf("got %s, want x264", enc.Name())
	}
	t.Setenv("AGORA_ENCODER", "vaapi")
	if _, err := NewEncoder(H264Options{Width: 320, Height: 240}); err == nil {
		t.Fatal("strict vaapi should fail without a device")
	}
}

func TestVAAPIColors(t *testing.T) {
	w, h := 256, 128
	cols := [][3]byte{{0, 0, 0}, {255, 255, 255}, {128, 128, 128}, {0, 0, 255}, {0, 255, 0}, {255, 0, 0}, {40, 200, 120}, {200, 60, 30}}
	f := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cols[x/32]
			o := (y*w + x) * 4
			f[o], f[o+1], f[o+2] = c[0], c[1], c[2]
		}
	}
	planes := func(enc Encoder) []byte {
		defer enc.Close()
		b, _, err := enc.Encode(f, w*4, w, h, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		return decodeNV12(t, b)
	}
	v, err := NewVAAPI(H264Options{Width: w, Height: h, CRF: 18})
	if err != nil {
		t.Skip(err)
	}
	x, _ := NewH264(H264Options{Width: w, Height: h, CRF: 18})
	a, b := planes(v), planes(x)
	for i := range cols {
		for _, o := range []int{64*w + i*32 + 16, w*h + 32*w + i*32 + 16, w*h + 32*w + i*32 + 17} {
			if d := int(a[o]) - int(b[o]); d > 3 || d < -3 {
				t.Fatalf("color %v: vaapi %d x264 %d", cols[i], a[o], b[o])
			}
		}
	}
}

func decodeNV12(t *testing.T, stream []byte) []byte {
	p := filepath.Join(t.TempDir(), "c.h264")
	os.WriteFile(p, stream, 0644)
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", p, "-f", "rawvideo", "-pix_fmt", "nv12", "-").Output()
	if err != nil {
		t.Skip(err)
	}
	return out
}
