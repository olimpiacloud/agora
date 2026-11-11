package core

import (
	"bytes"
	"image"
	"testing"
)

func TestDownscaleIntoReusesDestination(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 200))
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7)
	}
	want := Downscale(src, 120)
	dst := DownscaleInto(nil, src, 120)
	if !bytes.Equal(dst.Pix, want.Pix) || dst.Rect != want.Rect {
		t.Fatal("first DownscaleInto differs from Downscale")
	}
	for i := range src.Pix {
		src.Pix[i] = byte(i * 13)
	}
	want = Downscale(src, 120)
	again := DownscaleInto(dst, src, 120)
	if &again.Pix[0] != &dst.Pix[0] {
		t.Fatal("destination of the same size was reallocated")
	}
	if !bytes.Equal(again.Pix, want.Pix) {
		t.Fatal("reused DownscaleInto differs from Downscale")
	}
	if other := DownscaleInto(dst, src, 60); other.Rect.Dx() != 60 || &other.Pix[0] == &dst.Pix[0] {
		t.Fatal("new size must get a new destination")
	}
	if same := DownscaleInto(dst, src, 400); same != src {
		t.Fatal("a source narrower than maxW must be returned as is")
	}
}
