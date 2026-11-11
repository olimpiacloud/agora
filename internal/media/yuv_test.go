package media

import (
	"errors"
	"testing"
)

func TestBGRXShortSourceErrors(t *testing.T) {
	w, h := 64, 32
	y := make([]byte, w*h)
	u := make([]byte, w*h/4)
	v := make([]byte, w*h/4)
	uv := make([]byte, w*h/2)
	short := make([]byte, (h-1)*w*4)
	if err := BGRXToI420(short, w*4, w, h, y, w, u, v, w/2, w, h); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("i420 full: %v", err)
	}
	if err := BGRXToNV12(short, w*4, w, h, y, w, uv, w, w, h); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("nv12 full: %v", err)
	}
	if err := BGRXRectToI420(short, w*4, 0, 0, w, h, y, w, u, v, w/2); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("i420 rect: %v", err)
	}
	if err := BGRXRectToNV12(short, w*4, 0, 0, w, h, y, w, uv, w); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("nv12 rect: %v", err)
	}
	if err := BGRXRectToI420(make([]byte, 16*8*4), 32*4, 0, 0, 32, 16, y, w, u, v, w/2); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("i420 rect with frame trimmed to a smaller size: %v", err)
	}
	full := make([]byte, w*h*4)
	if err := BGRXToI420(full, w*4, w, h, y, w, u, v, w/2, w, h); err != nil {
		t.Fatal(err)
	}
	if err := BGRXRectToNV12(full, w*4, 0, 0, w, h, y, w, uv, w); err != nil {
		t.Fatal(err)
	}
}

func TestH264EncodeRectShortSource(t *testing.T) {
	enc, err := NewH264(H264Options{Width: 128, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if _, _, err := enc.EncodeRect(make([]byte, 32*8*4), 64*4, 0, 0, 64, 16, 0, false); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("EncodeRect: %v", err)
	}
	if _, _, err := enc.Encode(make([]byte, 100), 128*4, 128, 64, 0, true); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("Encode: %v", err)
	}
	if _, _, err := enc.Encode(make([]byte, 128*64*4), 128*4, 128, 64, 33, true); err != nil {
		t.Fatal(err)
	}
}
