package x11

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMPXConcurrentTyping(t *testing.T) {
	if _, err := exec.LookPath("xterm"); err != nil {
		t.Skip("xterm missing")
	}
	dir := t.TempDir()
	requireXvfb(t)
	d, err := StartDisplay(context.Background(), DisplayOptions{Width: 1280, Height: 800, RunDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	files := []string{dir + "/a.txt", dir + "/b.txt"}
	geo := []string{"60x10+0+0", "60x10+0+400"}
	var procs []*exec.Cmd
	for i := range files {
		c := exec.Command("xterm", "-geometry", geo[i], "-e", "sh", "-c", "cat > "+files[i])
		c.Env = append(os.Environ(), d.Env()...)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, c)
	}
	defer func() {
		for _, p := range procs {
			p.Process.Kill()
			p.Wait()
		}
	}()
	var ws []Window
	for i := 0; i < 100; i++ {
		ws, _ = d.Windows()
		if len(ws) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(ws) < 2 {
		t.Fatalf("windows: %+v", ws)
	}
	t.Logf("windows: %+v", ws)
	sa, err := d.NewSeat("agent-a")
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	sb, err := d.NewSeat("agent-b")
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()
	top := d.WindowAt(10, 10)
	bot := d.WindowAt(10, 410)
	sa.Click(50, 50, 1, 1)
	sb.Click(50, 450, 1, 1)
	if err := sa.Focus(xprotoWin(top)); err != nil {
		t.Fatal(err)
	}
	if err := sb.Focus(xprotoWin(bot)); err != nil {
		t.Fatal(err)
	}
	ta := "hello from seat A with Shift CAPS !@#\n"
	tb := "bonjour depuis le siege B 12345 ()\n"
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sa.Type(ta, 2*time.Millisecond) }()
	go func() { defer wg.Done(); sb.Type(tb, 2*time.Millisecond) }()
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
	a, _ := os.ReadFile(files[0])
	b, _ := os.ReadFile(files[1])
	t.Logf("A=%q B=%q", a, b)
	if strings.TrimSpace(string(a)) != strings.TrimSpace(ta) || strings.TrimSpace(string(b)) != strings.TrimSpace(tb) {
		t.Fatal("concurrent typing mixed or lost input")
	}
	ax, ay := sa.Position()
	bx, by := sb.Position()
	t.Logf("pointers A=(%d,%d) B=(%d,%d)", ax, ay, bx, by)
}

func requireXvfb(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb missing")
	}
}
