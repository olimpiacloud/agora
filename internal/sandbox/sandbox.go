package sandbox

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

const Cmd = "sandbox-exec"

var keepTop = map[string]bool{"usr": true, "etc": true, "opt": true, "bin": true, "sbin": true, "lib": true, "lib32": true, "lib64": true, "libx32": true, "sys": true, "dev": true, "proc": true, "var": true, "snap": true}

var hideVar = []string{"/var/lib", "/var/log", "/var/tmp", "/var/backups", "/var/mail", "/var/spool", "/var/www", "/var/opt"}

type Spec struct {
	Uid, Gid int
	Binds    []string
	Hide     []string
}

func Wrap(self string, s Spec) []string {
	args := []string{self, Cmd, "-uid", strconv.Itoa(s.Uid), "-gid", strconv.Itoa(s.Gid)}
	for _, b := range s.Binds {
		args = append(args, "-bind", b)
	}
	for _, h := range s.Hide {
		args = append(args, "-hide", h)
	}
	return append(args, "--")
}

func Attr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL, Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWIPC | syscall.CLONE_NEWUTS}
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
	fs := flag.NewFlagSet(Cmd, flag.ContinueOnError)
	var s Spec
	var binds, hide list
	fs.IntVar(&s.Uid, "uid", 0, "")
	fs.IntVar(&s.Gid, "gid", 0, "")
	fs.Var(&binds, "bind", "")
	fs.Var(&hide, "hide", "")
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
	s.Binds, s.Hide = binds, hide
	cwd, _ := os.Getwd()
	if err := setupRoot(s); err != nil {
		return err
	}
	if err := os.Chdir(cwd); err != nil {
		os.Chdir("/")
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
	unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0)
	return supervise(argv)
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
	if err := unix.MountSetattr(-1, r, unix.AT_RECURSIVE, &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY | unix.MOUNT_ATTR_NOSUID}); err != nil {
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
	if err := unix.Mount("tmpfs", r+"/dev/shm", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777"); err != nil {
		return fmt.Errorf("/dev/shm: %w", err)
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
	return unix.Mount(src, dst, "", unix.MS_BIND|unix.MS_REC, "")
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
