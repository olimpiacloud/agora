package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func setup(t *testing.T) (base, outside string) {
	t.Helper()
	root := t.TempDir()
	base = filepath.Join(root, "base")
	outside = filepath.Join(root, "outside")
	for _, d := range []string{base, outside} {
		if err := os.Mkdir(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	return base, outside
}

func owner(t *testing.T, p string) int {
	t.Helper()
	st, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return int(st.Sys().(*syscall.Stat_t).Uid)
}

func TestNormalPaths(t *testing.T) {
	base, _ := setup(t)
	if err := MkdirBeneath(base, "home/Downloads", 0700); err != nil {
		t.Fatal(err)
	}
	if err := MkdirBeneath(base, "home/Downloads", 0700); err != nil {
		t.Fatalf("second mkdir: %v", err)
	}
	if st, err := os.Stat(filepath.Join(base, "home", "Downloads")); err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		t.Fatalf("mkdir: %v %v", st, err)
	}
	if err := WriteFileBeneath(base, "home/prefs", []byte("long content"), 0600, -1, -1); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileBeneath(base, "home/prefs", []byte("short"), 0600, -1, -1); err != nil {
		t.Fatal(err)
	}
	b, err := ReadFileBeneath(base, "home/prefs")
	if err != nil || string(b) != "short" {
		t.Fatalf("read %q %v", b, err)
	}
	if err := ChownBeneath(base, "home", -1, -1); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := ChownBeneath(base, "home/Downloads", 4242, 4242); err != nil {
			t.Fatal(err)
		}
		if err := WriteFileBeneath(base, "home/prefs", []byte("x"), 0600, 4242, 4242); err != nil {
			t.Fatal(err)
		}
		if owner(t, filepath.Join(base, "home", "Downloads")) != 4242 || owner(t, filepath.Join(base, "home", "prefs")) != 4242 {
			t.Fatal("chown not applied")
		}
	}
}

func TestRejectsEscapes(t *testing.T) {
	base, _ := setup(t)
	for _, rel := range []string{"../outside/secret", "/etc/passwd", ".."} {
		if _, err := ReadFileBeneath(base, rel); !errors.Is(err, ErrUnsafe) {
			t.Errorf("%s: %v", rel, err)
		}
	}
}

func TestDirSymlink(t *testing.T) {
	base, outside := setup(t)
	if err := os.Symlink(outside, filepath.Join(base, "Default")); err != nil {
		t.Fatal(err)
	}
	before := owner(t, outside)
	if err := WriteFileBeneath(base, "Default/secret", []byte("pwned"), 0600, 4242, 4242); err == nil {
		t.Fatal("write through dir symlink succeeded")
	}
	if _, err := ReadFileBeneath(base, "Default/secret"); err == nil {
		t.Fatal("read through dir symlink succeeded")
	}
	if err := ChownBeneath(base, "Default", 4242, 4242); err == nil {
		t.Fatal("chown of dir symlink succeeded")
	}
	if err := MkdirBeneath(base, "Default/sub", 0700); err == nil {
		t.Fatal("mkdir through dir symlink succeeded")
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret")); string(b) != "keep" {
		t.Fatalf("outside file changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(outside, "sub")); err == nil {
		t.Fatal("outside dir created")
	}
	if owner(t, outside) != before {
		t.Fatal("outside dir owner changed")
	}
}

func TestFileSymlink(t *testing.T) {
	base, outside := setup(t)
	secret := filepath.Join(outside, "secret")
	if err := os.Symlink(secret, filepath.Join(base, "Preferences")); err != nil {
		t.Fatal(err)
	}
	before := owner(t, secret)
	if err := WriteFileBeneath(base, "Preferences", []byte("pwned"), 0600, 4242, 4242); err == nil {
		t.Fatal("write through file symlink succeeded")
	}
	if _, err := ReadFileBeneath(base, "Preferences"); err == nil {
		t.Fatal("read through file symlink succeeded")
	}
	if b, _ := os.ReadFile(secret); string(b) != "keep" {
		t.Fatalf("outside file changed: %q", b)
	}
	if owner(t, secret) != before {
		t.Fatal("outside file owner changed")
	}
}

func TestHardlinkAndFifo(t *testing.T) {
	base, outside := setup(t)
	if err := os.Link(filepath.Join(outside, "secret"), filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileBeneath(base, "linked", []byte("pwned"), 0600, -1, -1); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("hardlink: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret")); string(b) != "keep" {
		t.Fatalf("outside file changed: %q", b)
	}
	if err := unix.Mkfifo(filepath.Join(base, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileBeneath(base, "fifo"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("fifo read: %v", err)
	}
	if err := WriteFileBeneath(base, "fifo", nil, 0600, -1, -1); err == nil {
		t.Fatal("fifo write succeeded")
	}
}

func TestWalkFallback(t *testing.T) {
	base, outside := setup(t)
	if err := os.Mkdir(filepath.Join(base, "home"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "home", "link")); err != nil {
		t.Fatal(err)
	}
	bfd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(bfd)
	if _, err := walk(bfd, []string{"home", "link", "secret"}, unix.O_RDONLY|unix.O_CLOEXEC, 0); err == nil {
		t.Fatal("walk through dir symlink succeeded")
	}
	if _, err := walk(bfd, []string{"home", "link"}, unix.O_RDONLY|unix.O_CLOEXEC, 0); err == nil {
		t.Fatal("walk opened final symlink")
	}
	fd, err := walk(bfd, []string{"home", "new"}, unix.O_WRONLY|unix.O_CREAT|unix.O_CLOEXEC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	unix.Close(fd)
	if _, err := os.Stat(filepath.Join(base, "home", "new")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenat2Fallback(t *testing.T) {
	base, _ := setup(t)
	if err := os.WriteFile(filepath.Join(base, "f"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	orig := openat2
	t.Cleanup(func() {
		openat2 = orig
		noOpenat2.Store(false)
	})
	for _, errno := range []unix.Errno{unix.ENOSYS, unix.EPERM, unix.EINVAL, unix.E2BIG} {
		noOpenat2.Store(false)
		calls := 0
		openat2 = func(int, string, *unix.OpenHow) (int, error) {
			calls++
			return -1, errno
		}
		for i := 0; i < 3; i++ {
			b, err := ReadFileBeneath(base, "f")
			if err != nil || string(b) != "x" {
				t.Fatalf("%v: read %q %v", errno, b, err)
			}
		}
		if calls != 1 {
			t.Fatalf("%v: openat2 called %d times", errno, calls)
		}
	}
	noOpenat2.Store(false)
	calls := 0
	openat2 = func(int, string, *unix.OpenHow) (int, error) {
		calls++
		return -1, unix.ELOOP
	}
	for i := 0; i < 2; i++ {
		if _, err := ReadFileBeneath(base, "f"); !errors.Is(err, unix.ELOOP) {
			t.Fatalf("ELOOP fell back: %v", err)
		}
	}
	if calls != 2 || noOpenat2.Load() {
		t.Fatalf("ELOOP disabled openat2: calls %d", calls)
	}
}

func TestReadFileTooLarge(t *testing.T) {
	base, _ := setup(t)
	f, err := os.Create(filepath.Join(base, "big"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxReadSize + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ReadFileBeneath(base, "big"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("big read: %v", err)
	}
	if err := os.Truncate(filepath.Join(base, "big"), MaxReadSize); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFileBeneath(base, "big"); err != nil || len(b) != MaxReadSize {
		t.Fatalf("max read: %d %v", len(b), err)
	}
}
