package safefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var ErrUnsafe = errors.New("unsafe path")

func split(rel string) ([]string, error) {
	rel = filepath.Clean(rel)
	if rel == "." {
		return []string{"."}, nil
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("%w: %s", ErrUnsafe, rel)
	}
	return strings.Split(rel, "/"), nil
}

func OpenBeneath(base, rel string, flags int, mode os.FileMode) (*os.File, error) {
	parts, err := split(rel)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(base, rel)
	bfd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: base, Err: err}
	}
	defer unix.Close(bfd)
	flags |= unix.O_NOFOLLOW | unix.O_CLOEXEC
	how := &unix.OpenHow{Flags: uint64(flags), Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS}
	if flags&unix.O_CREAT != 0 {
		how.Mode = uint64(mode.Perm())
	}
	fd, err := unix.Openat2(bfd, strings.Join(parts, "/"), how)
	if err == unix.ENOSYS {
		fd, err = walk(bfd, parts, flags, uint32(mode.Perm()))
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

func walk(dfd int, parts []string, flags int, mode uint32) (int, error) {
	cur := dfd
	for i, p := range parts {
		var fd int
		var err error
		if i == len(parts)-1 {
			fd, err = unix.Openat(cur, p, flags|unix.O_NOFOLLOW, mode)
		} else {
			fd, err = unix.Openat(cur, p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if cur != dfd {
			unix.Close(cur)
		}
		if err != nil {
			return -1, err
		}
		cur = fd
	}
	return cur, nil
}

func MkdirBeneath(base, rel string, mode os.FileMode) error {
	parts, err := split(rel)
	if err != nil {
		return err
	}
	for i, p := range parts {
		if p == "." {
			continue
		}
		d, err := OpenBeneath(base, filepath.Join(parts[:i]...), unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		err = unix.Mkdirat(int(d.Fd()), p, uint32(mode.Perm()))
		d.Close()
		if err != nil && err != unix.EEXIST {
			return &os.PathError{Op: "mkdir", Path: filepath.Join(base, filepath.Join(parts[:i+1]...)), Err: err}
		}
	}
	d, err := OpenBeneath(base, rel, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	return d.Close()
}

func ChownBeneath(base, rel string, uid, gid int) error {
	d, err := OpenBeneath(base, rel, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Chown(uid, gid)
}

func regular(f *os.File) (*syscall.Stat_t, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrUnsafe, f.Name())
	}
	return fi.Sys().(*syscall.Stat_t), nil
}

func ReadFileBeneath(base, rel string) ([]byte, error) {
	f, err := OpenBeneath(base, rel, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := regular(f); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

func WriteFileBeneath(base, rel string, data []byte, mode os.FileMode, uid, gid int) error {
	f, err := OpenBeneath(base, rel, unix.O_WRONLY|unix.O_CREAT|unix.O_NONBLOCK, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := regular(f)
	if err != nil {
		return err
	}
	if st.Nlink > 1 {
		return fmt.Errorf("%w: %s has %d links", ErrUnsafe, f.Name(), st.Nlink)
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chown(uid, gid); err != nil {
		return err
	}
	return f.Close()
}
