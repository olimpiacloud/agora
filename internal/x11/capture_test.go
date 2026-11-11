package x11

import (
	"bytes"
	"testing"
)

func TestRGBAIntoReuse(t *testing.T) {
	f := &Frame{Width: 5, Height: 3, Stride: 24, Pix: make([]byte, 24*3)}
	for i := range f.Pix {
		f.Pix[i] = byte(i * 11)
	}
	want := f.RGBA()
	img := f.RGBAInto(nil)
	if !bytes.Equal(img.Pix, want.Pix) {
		t.Fatal("RGBAInto differs from RGBA")
	}
	for i := range f.Pix {
		f.Pix[i] = byte(i * 3)
	}
	want = f.RGBA()
	again := f.RGBAInto(img)
	if again != img || !bytes.Equal(again.Pix, want.Pix) {
		t.Fatal("reused RGBAInto differs from RGBA or reallocated")
	}
	f.Width = 4
	if other := f.RGBAInto(img); other == img || other.Rect.Dx() != 4 {
		t.Fatal("new size must get a new image")
	}
}
