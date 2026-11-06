package core

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type ProcUsage struct {
	Pid      int     `json:"pid"`
	Comm     string  `json:"comm"`
	Role     string  `json:"role,omitempty"`
	PSSKB    int64   `json:"pss_kb"`
	RSSKB    int64   `json:"rss_kb"`
	CPUTicks uint64  `json:"-"`
	CPU      float64 `json:"cpu_percent"`
}

var clkTck = 100.0

func procTable() map[int][]int {
	kids := map[int][]int{}
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		i := bytes.LastIndexByte(b, ')')
		if i < 0 || i+4 >= len(b) {
			continue
		}
		f := strings.Fields(string(b[i+2:]))
		if len(f) < 2 {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		kids[ppid] = append(kids[ppid], pid)
	}
	return kids
}

func Descendants(roots []int) []int {
	kids := procTable()
	seen := map[int]bool{}
	var out []int
	var walk func(int)
	walk = func(p int) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
		for _, c := range kids[p] {
			walk(c)
		}
	}
	for _, r := range roots {
		if r > 0 {
			if _, err := os.Stat(fmt.Sprintf("/proc/%d", r)); err == nil {
				walk(r)
			}
		}
	}
	return out
}

func ReadUsage(pid int) (ProcUsage, error) {
	u := ProcUsage{Pid: pid}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return u, err
	}
	l := bytes.IndexByte(b, '(')
	r := bytes.LastIndexByte(b, ')')
	if l < 0 || r < 0 {
		return u, fmt.Errorf("bad stat")
	}
	u.Comm = string(b[l+1 : r])
	f := strings.Fields(string(b[r+2:]))
	if len(f) > 13 {
		ut, _ := strconv.ParseUint(f[11], 10, 64)
		st, _ := strconv.ParseUint(f[12], 10, 64)
		u.CPUTicks = ut + st
	}
	fh, err := os.Open(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err == nil {
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "Pss:") {
				u.PSSKB = kb(line)
			} else if strings.HasPrefix(line, "Rss:") {
				u.RSSKB = kb(line)
			}
		}
		fh.Close()
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		if bytes.Contains(b, []byte("--type=")) {
			for _, a := range bytes.Split(b, []byte{0}) {
				if bytes.HasPrefix(a, []byte("--type=")) {
					u.Role = "chrome-" + string(a[7:])
				}
			}
		}
	}
	return u, nil
}

func kb(line string) int64 {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0
	}
	v, _ := strconv.ParseInt(f[1], 10, 64)
	return v
}

func SelfUsage() ProcUsage {
	u, _ := ReadUsage(os.Getpid())
	return u
}

func SignalTree(pids []int, sig syscall.Signal) {
	for _, p := range pids {
		syscall.Kill(p, sig)
	}
}

type Cgroups struct {
	root string
}

func DetectCgroups() *Cgroups {
	if os.Getenv("CARBON_CGROUPS") == "off" {
		return nil
	}
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil
	}
	var rel string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(l, "0::") {
			rel = strings.TrimPrefix(l, "0::")
		}
	}
	if rel == "" || !strings.HasSuffix(rel, "carbon.service") && !strings.HasSuffix(rel, "carbon.service/daemon") {
		return nil
	}
	rel = strings.TrimSuffix(rel, "/daemon")
	root := filepath.Join("/sys/fs/cgroup", rel)
	if err := os.MkdirAll(filepath.Join(root, "daemon"), 0755); err != nil {
		return nil
	}
	if err := os.WriteFile(filepath.Join(root, "daemon", "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return nil
	}
	os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("+memory +cpu +pids"), 0644)
	return &Cgroups{root: root}
}

func (c *Cgroups) path(ws string) string { return filepath.Join(c.root, "ws-"+ws) }

func (c *Cgroups) Ensure(ws string, b Budget) error {
	p := c.path(ws)
	if err := os.MkdirAll(p, 0755); err != nil {
		return err
	}
	if b.MemoryMB > 0 {
		os.WriteFile(filepath.Join(p, "memory.max"), []byte(strconv.Itoa(b.MemoryMB<<20)), 0644)
		os.WriteFile(filepath.Join(p, "memory.high"), []byte(strconv.Itoa(b.MemoryMB<<20*9/10)), 0644)
	}
	if b.CPUPercent > 0 {
		os.WriteFile(filepath.Join(p, "cpu.max"), []byte(fmt.Sprintf("%d 100000", b.CPUPercent*1000)), 0644)
	}
	return nil
}

func (c *Cgroups) Add(ws string, pid int) error {
	return os.WriteFile(filepath.Join(c.path(ws), "cgroup.procs"), []byte(strconv.Itoa(pid)), 0644)
}

func (c *Cgroups) Freeze(ws string, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return os.WriteFile(filepath.Join(c.path(ws), "cgroup.freeze"), []byte(v), 0644)
}

func (c *Cgroups) Remove(ws string) {
	os.Remove(c.path(ws))
}

func (c *Cgroups) Memory(ws string) int64 {
	b, err := os.ReadFile(filepath.Join(c.path(ws), "memory.current"))
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return v
}

func StatLight(pid int) (uint64, int64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, err
	}
	r := bytes.LastIndexByte(b, ')')
	if r < 0 {
		return 0, 0, fmt.Errorf("bad stat")
	}
	f := strings.Fields(string(b[r+2:]))
	if len(f) < 22 {
		return 0, 0, fmt.Errorf("short stat")
	}
	ut, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	rss, _ := strconv.ParseInt(f[21], 10, 64)
	return ut + st, rss * int64(os.Getpagesize()) / 1024, nil
}

func (c *Cgroups) FD(ws string) (int, func()) {
	f, err := os.Open(c.path(ws))
	if err != nil {
		return -1, func() {}
	}
	return int(f.Fd()), func() { f.Close() }
}

func (c *Cgroups) Pids(ws string) []int {
	b, err := os.ReadFile(filepath.Join(c.path(ws), "cgroup.procs"))
	if err != nil {
		return nil
	}
	var out []int
	for _, l := range strings.Fields(string(b)) {
		if p, err := strconv.Atoi(l); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func (c *Cgroups) CPUUsec(ws string) uint64 {
	b, err := os.ReadFile(filepath.Join(c.path(ws), "cpu.stat"))
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "usage_usec ") {
			v, _ := strconv.ParseUint(strings.TrimPrefix(l, "usage_usec "), 10, 64)
			return v
		}
	}
	return 0
}
