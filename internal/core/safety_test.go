package core

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAppIDValidation(t *testing.T) {
	for _, id := range []string{"../x", "a/b", " ok ", "..", ".hidden", "-x"} {
		if checkAppID(id) == nil {
			t.Errorf("accepted %q", id)
		}
		if _, err := (&Manager{}).LaunchApp(context.Background(), "ws", AppSpec{ID: id, Command: "true"}); err == nil {
			t.Errorf("LaunchApp accepted %q", id)
		}
		if _, err := (&Runtime{}).launch(AppSpec{ID: id, Command: "true"}); err == nil {
			t.Errorf("launch accepted %q", id)
		}
	}
	for _, id := range []string{"my-app_1.2", "", NewID("app")} {
		if err := checkAppID(id); err != nil {
			t.Errorf("rejected %q: %v", id, err)
		}
	}
}

func TestPrepareWSDir(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	home := t.TempDir()
	outside := t.TempDir()
	m := &Manager{cfg: Config{Home: home}, uid: 4242, gid: 4242}
	dir := m.wsDir("ws1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	os.Chown(dir, 4242, 4242)
	if err := os.Symlink(outside, filepath.Join(dir, "run")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "home")); err != nil {
		t.Fatal(err)
	}
	if err := m.prepareWSDir("ws1"); err != nil {
		t.Fatal(err)
	}
	own := func(p string) (uint32, os.FileMode) {
		st, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return st.Sys().(*syscall.Stat_t).Uid, st.Mode()
	}
	if u, mode := own(dir); u != 0 || mode.Perm() != 0711 {
		t.Fatalf("workspace dir uid %d mode %v", u, mode)
	}
	if u, mode := own(filepath.Join(dir, "run")); u != 0 || !mode.IsDir() || mode.Perm() != 0711 {
		t.Fatalf("run uid %d mode %v", u, mode)
	}
	for _, rel := range []string{"home", "home/Downloads", "home/.xdg-run"} {
		if u, mode := own(filepath.Join(dir, rel)); u != 4242 || !mode.IsDir() {
			t.Fatalf("%s uid %d mode %v", rel, u, mode)
		}
	}
	if u, _ := own(outside); u != 0 {
		t.Fatalf("outside dir chowned to %d", u)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("outside dir written: %v", ents)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "home", "Downloads2")); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "home", "Downloads"))
	if err := os.Rename(filepath.Join(dir, "home", "Downloads2"), filepath.Join(dir, "home", "Downloads")); err != nil {
		t.Fatal(err)
	}
	if err := m.prepareWSDir("ws1"); err == nil {
		t.Fatal("prepareWSDir followed a Downloads symlink")
	}
	if u, _ := own(outside); u != 0 {
		t.Fatalf("outside dir chowned to %d", u)
	}
}
