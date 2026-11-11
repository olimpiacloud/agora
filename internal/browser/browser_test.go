package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/x11"
)

func appUser(t *testing.T) (uint32, uint32) {
	if os.Getuid() != 0 {
		return 0, 0
	}
	u, err := user.Lookup("agora")
	if err != nil {
		t.Skip("no agora user")
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uint32(uid), uint32(gid)
}

func TestBrowserSemantic(t *testing.T) {
	requireXvfb(t)
	uid, gid := appUser(t)
	dir, _ := os.MkdirTemp("/tmp", "agora-bt-")
	defer os.RemoveAll(dir)
	os.Chown(dir, int(uid), int(gid))
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 1280, Height: 800, RunDir: dir + "/run", AuthUid: int(uid), AuthGid: int(gid)})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	os.Chown(d.AuthFile, int(uid), int(gid))
	wm, err := d.StartWM(x11.LayoutTile)
	if err != nil {
		t.Fatal(err)
	}
	defer wm.Close()
	env := append([]string{"HOME=" + dir, "DBUS_SESSION_BUS_ADDRESS=disabled:"}, d.Env()...)
	ctx := context.Background()
	t0 := time.Now()
	b, err := Launch(ctx, LaunchOptions{UserDataDir: dir + "/profile", Env: env, Uid: uid, Gid: gid, LogPath: dir + "/chrome.log"})
	if err != nil {
		lg, _ := os.ReadFile(dir + "/chrome.log")
		t.Fatal(err, string(lg))
	}
	defer b.Close(ctx)
	t.Logf("launch %v", time.Since(t0))
	page := `data:text/html,<title>T</title><input id=a placeholder=first><textarea id=b placeholder=second></textarea><button onclick="document.title='clicked:'+a.value+'|'+b.value">Go</button><input type=password value=hunter2><input autocomplete=one-time-code value=123456><div role=tab tabindex=0 aria-selected=true>Tab1</div><button aria-expanded=false>Menu</button><button aria-disabled=true>Off</button><div role=checkbox tabindex=0 aria-checked=true>Box</div><button onclick="document.title=confirm('Delete?')?'yes':'no'">Del</button>`
	tab, err := b.NewTab(ctx, NewTabOptions{URL: page, NewWindow: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	s, err := tab.Snapshot(ctx, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(s.Compact(true))
	var in, ta, btn, del string
	for _, e := range s.Elements {
		switch {
		case e.Name == "first":
			in = e.Ref
		case e.Name == "second":
			ta = e.Ref
		case e.Role == "button" && e.Name == "Go":
			btn = e.Ref
		case e.Role == "button" && e.Name == "Del":
			del = e.Ref
		}
	}
	compact := s.Compact(false)
	compact = compact[strings.Index(compact, "\ntitle:"):]
	if strings.Contains(compact, "hunter2") || strings.Contains(compact, "123456") {
		t.Fatalf("secret leaked: %s", compact)
	}
	for _, want := range []string{`tab "Tab1" selected`, `button "Menu" collapsed`, `button "Off" disabled`, `checkbox "Box" checked`} {
		if !strings.Contains(compact, want) {
			t.Fatalf("missing %q in %s", want, compact)
		}
	}
	if err := tab.FillRef(ctx, in, "hola", false); err != nil {
		t.Fatal(err)
	}
	if err := tab.FillRef(ctx, ta, "mundo", false); err != nil {
		t.Fatal(err)
	}
	if err := tab.ClickRef(ctx, btn, "", 1); err != nil {
		t.Fatal(err)
	}
	var title string
	tab.Eval(ctx, "document.title", &title)
	if title != "clicked:hola|mundo" {
		t.Fatalf("title=%q", title)
	}
	dctx, dcancel := tab.DialogContext(ctx)
	t1 := time.Now()
	err = tab.ClickRef(dctx, del, "", 1)
	if err == nil {
		err = tab.Eval(dctx, "1", nil)
	}
	dcancel()
	if !errors.Is(context.Cause(dctx), ErrDialogOpen) || err == nil || time.Since(t1) > 5*time.Second {
		t.Fatalf("dialog did not cancel the call: %v %v after %v", err, context.Cause(dctx), time.Since(t1))
	}
	if d := tab.Dialog(); d == nil || d["message"] != "Delete?" || d["type"] != "confirm" {
		t.Fatalf("dialog %v", d)
	}
	if err := tab.HandleDialog(ctx, true, ""); err != nil {
		t.Fatal(err)
	}
	tab.Eval(ctx, "document.title", &title)
	if title != "yes" || tab.Dialog() != nil {
		t.Fatalf("after accept title=%q dialog=%v", title, tab.Dialog())
	}
	t1 = time.Now()
	if err := tab.Navigate(ctx, page+"#frag", "", 10*time.Second); err != nil || time.Since(t1) > 3*time.Second {
		t.Fatalf("same-document navigate: %v after %v", err, time.Since(t1))
	}
	if err := tab.Navigate(ctx, "file:///etc/passwd", "", time.Second); err == nil {
		t.Fatal("file URL navigated")
	}
	tab2, err := b.NewTab(ctx, NewTabOptions{URL: page, NewWindow: true, Isolated: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	ws, _ := d.Windows()
	t.Logf("x windows: %d", len(ws))
	for _, w := range ws {
		t.Logf("  %+v", w)
	}
	png, err := tab2.Screenshot(ctx, "png", 0, false)
	if err != nil || len(png) < 1000 {
		t.Fatalf("screenshot %d %v", len(png), err)
	}
	restore, err := tab2.WindowTitleMarker(ctx, "agora-marker-xyz")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	ws, _ = d.Windows()
	found := false
	for _, w := range ws {
		if strings.Contains(w.Title, "agora-marker-xyz") {
			found = true
		}
	}
	restore()
	if !found {
		t.Fatal("marker window not found")
	}
}

func requireXvfb(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb missing")
	}
}

func (t *Tab) WindowTitleMarker(ctx context.Context, marker string) (func(), error) {
	var prev string
	if err := t.Eval(ctx, fmt.Sprintf("(()=>{const p=document.title;document.title=%q;return p})()", marker), &prev); err != nil {
		return nil, err
	}
	return func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		b, _ := json.Marshal(prev)
		t.Eval(c, fmt.Sprintf("document.title=%s", b), nil)
	}, nil
}

func launchWith(t *testing.T, extra []string) *Browser {
	t.Helper()
	requireXvfb(t)
	uid, gid := appUser(t)
	dir, err := os.MkdirTemp("/tmp", "agora-bt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chown(dir, int(uid), int(gid))
	d, err := x11.StartDisplay(context.Background(), x11.DisplayOptions{Width: 1280, Height: 800, RunDir: dir + "/run", AuthUid: int(uid), AuthGid: int(gid)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	os.Chown(d.AuthFile, int(uid), int(gid))
	wm, err := d.StartWM(x11.LayoutTile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { wm.Close() })
	env := append([]string{"HOME=" + dir, "DBUS_SESSION_BUS_ADDRESS=disabled:"}, d.Env()...)
	ctx := context.Background()
	b, err := Launch(ctx, LaunchOptions{UserDataDir: dir + "/profile", Env: env, Uid: uid, Gid: gid, LogPath: dir + "/chrome.log", ExtraArgs: extra})
	if err != nil {
		lg, _ := os.ReadFile(dir + "/chrome.log")
		t.Fatal(err, string(lg))
	}
	t.Cleanup(func() { b.Close(ctx) })
	return b
}

const webglProbe = `(() => { const c = document.createElement('canvas'); const gl = c.getContext('webgl2') || c.getContext('webgl'); if (!gl) return ''; const e = gl.getExtension('WEBGL_debug_renderer_info'); return e ? String(gl.getParameter(e.UNMASKED_RENDERER_WEBGL)) : 'webgl'; })()`

func probeWebGL(t *testing.T, extra []string) string {
	t.Helper()
	b := launchWith(t, extra)
	ctx := context.Background()
	tab, err := b.NewTab(ctx, NewTabOptions{URL: "data:text/html,<title>gl</title><canvas id=c></canvas>", NewWindow: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	var out string
	if err := tab.Eval(ctx, webglProbe, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSoftwareWebGL(t *testing.T) {
	requireXvfb(t)
	candidates := [][]string{
		nil,
		{"--enable-unsafe-swiftshader"},
		{"--use-gl=angle", "--use-angle=swiftshader", "--enable-unsafe-swiftshader"},
		{"--use-gl=angle", "--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"},
	}
	worked := 0
	for i, extra := range candidates {
		t.Run(fmt.Sprintf("candidate%d", i+1), func(t *testing.T) {
			r := probeWebGL(t, extra)
			if r == "" {
				t.Logf("flags %q: no context", extra)
				return
			}
			t.Logf("flags %q: %s", extra, r)
			worked++
		})
	}
	if worked == 0 {
		t.Fatal("no candidate flag set gave a WebGL context")
	}
	args, err := WebGLArgs("software")
	if err != nil {
		t.Fatal(err)
	}
	r := probeWebGL(t, args)
	if r == "" {
		t.Fatalf("WebGLArgs(\"software\") %q gave no WebGL context", args)
	}
	t.Logf("software: %s", r)
}
