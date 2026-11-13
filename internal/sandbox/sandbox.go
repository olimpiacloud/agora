package sandbox

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const Cmd = "sandbox-exec"

var keepTop = map[string]bool{"usr": true, "etc": true, "opt": true, "bin": true, "sbin": true, "lib": true, "lib32": true, "lib64": true, "libx32": true, "sys": true, "dev": true, "proc": true, "var": true, "snap": true}

var hideVar = []string{"/var/lib", "/var/log", "/var/tmp", "/var/backups", "/var/mail", "/var/spool", "/var/www", "/var/opt"}

var rootAttr uint64 = unix.MOUNT_ATTR_RDONLY | unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NODEV

var devNodes = []string{"null", "zero", "full", "random", "urandom", "tty"}

var devLinks = [][2]string{{"ptmx", "pts/ptmx"}, {"fd", "/proc/self/fd"}, {"stdin", "/proc/self/fd/0"}, {"stdout", "/proc/self/fd/1"}, {"stderr", "/proc/self/fd/2"}}

type mnt struct {
	src, dst, fstype string
	flags            uintptr
	data             string
}

type Spec struct {
	Uid, Gid int
	Binds    []string
	Hide     []string
	NetNS    string
}

func Wrap(self string, s Spec) []string {
	args := []string{self, Cmd, "-uid", strconv.Itoa(s.Uid), "-gid", strconv.Itoa(s.Gid)}
	for _, b := range s.Binds {
		args = append(args, "-bind", b)
	}
	for _, h := range s.Hide {
		args = append(args, "-hide", h)
	}
	if s.NetNS != "" {
		args = append(args, "-netns", s.NetNS)
	}
	return append(args, "--")
}

func Attr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL, Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWIPC | syscall.CLONE_NEWUTS}
}

func Probe(self string, uid, gid int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	argv := append(Wrap(self, Spec{Uid: uid, Gid: gid}), "true")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = Attr()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox probe: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func WaitSignals(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for !catchesTerm(pid) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func catchesTerm(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return true
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "State:"); ok && strings.HasPrefix(strings.TrimSpace(v), "Z") {
			return true
		}
		if v, ok := strings.CutPrefix(l, "SigCgt:"); ok {
			m, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			return err != nil || m&(1<<(uint(syscall.SIGTERM)-1)) != 0
		}
	}
	return true
}

type list []string

func (l *list) String() string     { return fmt.Sprint(*l) }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func Main(args []string) {
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "agora sandbox:", err)
		os.Exit(126)
	}
}

func run(args []string) error {
	runtime.LockOSThread()
	fs := flag.NewFlagSet(Cmd, flag.ContinueOnError)
	var s Spec
	var binds, hide list
	var netns string
	fs.IntVar(&s.Uid, "uid", 0, "")
	fs.IntVar(&s.Gid, "gid", 0, "")
	fs.Var(&binds, "bind", "")
	fs.Var(&hide, "hide", "")
	fs.StringVar(&netns, "netns", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) == 0 || s.Uid == 0 || s.Gid == 0 {
		return errors.New("usage: sandbox-exec -uid U -gid G [-bind P]... [-hide P]... -- cmd args")
	}
	if os.Getpid() != 1 {
		return errors.New("must run as pid 1 of a new pid namespace")
	}
	if netns != "" {
		if err := joinNetNS(netns); err != nil {
			return fmt.Errorf("join network namespace %s: %w", netns, err)
		}
	}
	s.Binds, s.Hide = binds, hide
	cwd, _ := os.Getwd()
	if err := setupRoot(s); err != nil {
		return err
	}
	if err := os.Chdir(cwd); err != nil {
		os.Chdir("/")
	}
	if err := dropBounding(); err != nil {
		return err
	}
	if err := syscall.Setgroups(nil); err != nil {
		return err
	}
	if err := syscall.Setresgid(s.Gid, s.Gid, s.Gid); err != nil {
		return err
	}
	if err := syscall.Setresuid(s.Uid, s.Uid, s.Uid); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0)
	return supervise(argv)
}

func joinNetNS(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = unix.Setns(int(f.Fd()), unix.CLONE_NEWNET)
	f.Close()
	if err != nil {
		return err
	}
	return loopbackUp()
}

func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}

func dropBounding() error {
	for c := uintptr(0); c < 64; c++ {
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, c, 0, 0, 0); err != nil {
			if err == unix.EINVAL && c > 0 {
				return nil
			}
			return fmt.Errorf("drop capability %d from the bounding set: %w", c, err)
		}
	}
	return nil
}

func setupRoot(s Spec) error {
	srcs := make([]*os.File, len(s.Binds))
	for i, b := range s.Binds {
		f, err := os.OpenFile(b, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("bind %s: %w", b, err)
		}
		defer f.Close()
		srcs[i] = f
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make / private: %w", err)
	}
	if err := unix.Mount("tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0755,size=16m"); err != nil {
		return fmt.Errorf("staging tmpfs: %w", err)
	}
	r := "/tmp/root"
	if err := os.Mkdir(r, 0755); err != nil {
		return err
	}
	if err := unix.Mount("/", r, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("bind /: %w", err)
	}
	if err := unix.MountSetattr(-1, r, unix.AT_RECURSIVE, &unix.MountAttr{Attr_set: rootAttr}); err != nil {
		return fmt.Errorf("read-only root: %w", err)
	}
	ents, err := os.ReadDir("/")
	if err != nil {
		return err
	}
	var hide []string
	for _, e := range ents {
		if !keepTop[e.Name()] && e.IsDir() {
			hide = append(hide, "/"+e.Name())
		}
	}
	hide = append(hide, hideVar...)
	hide = append(hide, s.Hide...)
	for _, h := range hide {
		if st, err := os.Lstat(r + h); err != nil || !st.IsDir() {
			continue
		}
		if err := unix.Mount("tmpfs", r+h, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0755,size=64m"); err != nil {
			return fmt.Errorf("hide %s: %w", h, err)
		}
	}
	if err := setupDev(r + "/dev"); err != nil {
		return err
	}
	if err := unix.Mount("proc", r+"/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		return fmt.Errorf("/proc: %w", err)
	}
	os.Chmod(r+"/tmp", 01777)
	for i, b := range s.Binds {
		if err := bind(srcs[i], r+b); err != nil {
			return fmt.Errorf("bind %s: %w", b, err)
		}
	}
	if err := os.Chdir(r); err != nil {
		return err
	}
	if err := unix.PivotRoot(".", "."); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	if err := unix.Unmount(".", unix.MNT_DETACH); err != nil {
		return fmt.Errorf("detach old root: %w", err)
	}
	return os.Chdir("/")
}

func devMounts(dev string) []mnt {
	ms := []mnt{{"tmpfs", dev, "tmpfs", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, "mode=0755,size=64k"}}
	for _, n := range devNodes {
		ms = append(ms, mnt{"/dev/" + n, dev + "/" + n, "", unix.MS_BIND, ""})
	}
	return append(ms,
		mnt{"devpts", dev + "/pts", "devpts", unix.MS_NOSUID | unix.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=0620,gid=5"},
		mnt{"tmpfs", dev + "/shm", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=1777"},
	)
}

func setupDev(dev string) error {
	if err := unix.Unmount(dev, unix.MNT_DETACH); err != nil && err != unix.EINVAL {
		return fmt.Errorf("detach host /dev: %w", err)
	}
	for i, m := range devMounts(dev) {
		if i > 0 {
			if err := mountpoint(m); err != nil {
				return fmt.Errorf("%s: %w", m.dst, err)
			}
		}
		if err := unix.Mount(m.src, m.dst, m.fstype, m.flags, m.data); err != nil {
			return fmt.Errorf("mount %s: %w", m.dst, err)
		}
	}
	for _, l := range devLinks {
		if err := os.Symlink(l[1], filepath.Join(dev, l[0])); err != nil {
			return err
		}
	}
	return nil
}

func mountpoint(m mnt) error {
	if m.fstype != "" {
		return os.Mkdir(m.dst, 0755)
	}
	st, err := os.Stat(m.src)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s is not a character device", m.src)
	}
	f, err := os.OpenFile(m.dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

func bind(f *os.File, dst string) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	src := fmt.Sprintf("/proc/self/fd/%d", f.Fd())
	if st.IsDir() {
		if err := mkdirAll(dst); err != nil {
			return err
		}
	} else {
		if err := mkdirAll(filepath.Dir(dst)); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		f.Close()
	}
	if err := unix.Mount(src, dst, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return err
	}
	return unix.MountSetattr(-1, dst, unix.AT_RECURSIVE, &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NODEV})
}

func mkdirAll(p string) error {
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return os.MkdirAll(p, 0755)
}

func supervise(argv []string) error {
	bin := argv[0]
	if filepath.Base(bin) == bin {
		p, err := lookPath(bin)
		if err != nil {
			return err
		}
		bin = p
	}
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGCHLD)
	files := []uintptr{0, 1, 2}
	for fd := uintptr(3); fd < 16; fd++ {
		if _, err := unix.FcntlInt(fd, unix.F_GETFD, 0); err != nil {
			break
		}
		files = append(files, fd)
	}
	cwd, _ := os.Getwd()
	pid, err := syscall.ForkExec(bin, argv, &syscall.ProcAttr{Dir: cwd, Env: os.Environ(), Files: files, Sys: &syscall.SysProcAttr{Setsid: true}})
	if err != nil {
		return err
	}
	for sig := range sigs {
		if sig != syscall.SIGCHLD {
			syscall.Kill(-pid, sig.(syscall.Signal))
			continue
		}
		for {
			var ws syscall.WaitStatus
			p, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
			if p <= 0 || err != nil {
				break
			}
			if p == pid {
				if ws.Signaled() {
					os.Exit(128 + int(ws.Signal()))
				}
				os.Exit(ws.ExitStatus())
			}
		}
	}
	return nil
}

func lookPath(name string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: not found in PATH", name)
}
