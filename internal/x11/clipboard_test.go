package x11

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestClipboardLimits(t *testing.T) {
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb missing")
	}
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 640, Height: 480, RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	owner, err := d.NewClipboard()
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	reader, err := d.NewClipboard()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	if err := owner.Set(strings.Repeat("x", 300<<10)); err == nil {
		t.Fatal("oversized clipboard text was accepted")
	}
	big := strings.Repeat("ab", MaxClipboard/2)
	if err := owner.Set(big); err != nil {
		t.Fatalf("set at limit: %v", err)
	}
	got, err := reader.Get(3 * time.Second)
	if err != nil || got != big {
		t.Fatalf("read at limit: %d bytes, err %v", len(got), err)
	}
	if err := owner.Set("small"); err != nil {
		t.Fatalf("connection broken after limit: %v", err)
	}
	if got, err := reader.Get(3 * time.Second); err != nil || got != "small" {
		t.Fatalf("read after limit: %q %v", got, err)
	}
}
