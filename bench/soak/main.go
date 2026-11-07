package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/res"
	"github.com/olimpiacloud/agora/internal/client"
	"github.com/olimpiacloud/agora/internal/core"
)

var realPages = []string{
	"https://example.com/",
	"https://en.wikipedia.org/wiki/Special:Random",
	"https://news.ycombinator.com/",
	"https://developer.mozilla.org/en-US/docs/Web/HTML",
	"https://go.dev/doc/",
}

const localPage = `<title>soak %d</title><body style="font:16px sans-serif"><h1 id=h>soak page %d</h1><input id=i><textarea id=t rows=5 cols=60></textarea><div id=d></div><canvas id=c width=400 height=200></canvas><script>const x=c.getContext('2d');let n=0;setInterval(()=>{n++;x.fillStyle='hsl('+(n*7%%360)+',60%%,50%%)';x.fillRect(n*5%%380,n*3%%180,20,20);d.textContent=new Date().toISOString()},100);for(let k=0;k<200;k++){const p=document.createElement('p');p.textContent='paragraph '+k+' '+'lorem ipsum '.repeat(k%%9+1);document.body.appendChild(p)}</script>`

type metric struct {
	name   string
	unit   string
	absTol float64
	relTol float64
}

var metrics = []metric{
	{"daemon_rss_mb", "MB", 25, 0.25},
	{"daemon_heap_mb", "MB", 15, 0.30},
	{"goroutines", "", 20, 0.20},
	{"daemon_fds", "", 12, 0.15},
	{"xvfb_rss_mb", "MB", 40, 0.30},
	{"chrome_pss_mb", "MB", 250, 0.40},
	{"x_clients", "", 4, 0.30},
	{"x_resources", "", 300, 0.30},
}

type sample struct {
	t time.Duration
	v map[string]float64
}

var (
	apiAddr string
	admin   *client.Client
	daemon  *exec.Cmd
	gate    sync.RWMutex
	ops     sync.Map
	errs    sync.Map
	ws      string
)

func count(m *sync.Map, k string) {
	v, _ := m.LoadOrStore(k, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func freePort() int {
	for p := 7800 + rand.IntN(100); ; p = 7800 + rand.IntN(100) {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			l.Close()
			return p
		}
	}
}

func do(c *client.Client, op, method, path string, body any) map[string]any {
	gate.RLock()
	defer gate.RUnlock()
	return doLocked(c, op, method, path, body)
}

func doLocked(c *client.Client, op, method, path string, body any) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out map[string]any
	count(&ops, op)
	if err := c.Do(ctx, method, path, body, &out); err != nil {
		count(&errs, op)
		log.Printf("%s: %v", op, err)
		return nil
	}
	return out
}

func sleep(end time.Time, d time.Duration) bool {
	if time.Now().Add(d).After(end) {
		return false
	}
	time.Sleep(d)
	return true
}

func browse(id int, agent *client.Client, local string, real bool, end time.Time) {
	for k := 0; time.Now().Before(end); k++ {
		url := fmt.Sprintf("%s/p/%d", local, k)
		if real && k%3 == 0 {
			url = realPages[rand.IntN(len(realPages))]
		}
		tab := do(agent, "tab.open", "POST", "/v1/workspaces/"+ws+"/tabs", map[string]any{"url": url, "same_window": k%4 != 0})
		if tab == nil {
			sleep(end, 2*time.Second)
			continue
		}
		target, _ := tab["target"].(string)
		act := func(op string, body map[string]any) {
			do(agent, "tab."+op, "POST", "/v1/workspaces/"+ws+"/tabs/"+target, body)
		}
		act("wait", map[string]any{"action": "wait", "selector": "body", "timeout_ms": 15000})
		act("snapshot", map[string]any{"action": "snapshot", "max_text": 1500})
		act("scroll", map[string]any{"action": "scroll", "dy": 600})
		if !strings.HasPrefix(url, "https://") {
			act("focus", map[string]any{"action": "eval", "expression": "document.getElementById('t').focus()"})
			act("type", map[string]any{"action": "insert_text", "text": fmt.Sprintf("cursor %d iteration %d", id, k)})
			act("eval", map[string]any{"action": "eval", "expression": "document.querySelectorAll('p').length"})
			act("navigate", map[string]any{"action": "navigate", "url": fmt.Sprintf("%s/p/%d-b", local, k)})
		} else {
			act("press", map[string]any{"action": "press", "keys": "End"})
		}
		if k%5 == 0 {
			act("screenshot", map[string]any{"action": "screenshot"})
		}
		sleep(end, time.Duration(500+rand.IntN(1500))*time.Millisecond)
		act("close", map[string]any{"action": "close"})
		sleep(end, time.Duration(300+rand.IntN(700))*time.Millisecond)
	}
}

func desktop(agent *client.Client, end time.Time) {
	apps := [][]string{{"xclock", "-update", "1"}, {"xterm"}, {"xeyes"}, {"xlogo"}}
	for k := 0; time.Now().Before(end); k++ {
		a := apps[k%len(apps)]
		p := do(agent, "app.launch", "POST", "/v1/workspaces/"+ws+"/apps", map[string]any{"command": a[0], "args": a[1:]})
		sleep(end, 2*time.Second)
		for i := 0; i < 5; i++ {
			do(agent, "input.move", "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "move", "x": 100 + rand.IntN(800), "y": 100 + rand.IntN(600)})
		}
		do(agent, "input.click", "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "click", "x": 300, "y": 300})
		do(agent, "input.type", "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "type", "text": "echo soak\n"})
		gate.RLock()
		var wins []map[string]any
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		count(&ops, "windows.list")
		if err := agent.Do(ctx, "GET", "/v1/workspaces/"+ws+"/windows", nil, &wins); err != nil {
			count(&errs, "windows.list")
		}
		cancel()
		gate.RUnlock()
		for _, w := range wins {
			title, _ := w["title"].(string)
			id, _ := w["id"].(float64)
			if id != 0 && (strings.Contains(strings.ToLower(title), a[0]) || title == "xterm") {
				do(agent, "window.op", "POST", fmt.Sprintf("/v1/workspaces/%s/windows/%d", ws, uint32(id)), map[string]any{"action": "move", "x": rand.IntN(400), "y": rand.IntN(300), "width": 400, "height": 300})
				do(agent, "window.close", "POST", fmt.Sprintf("/v1/workspaces/%s/windows/%d", ws, uint32(id)), map[string]any{"action": "close"})
			}
		}
		sleep(end, time.Second)
		if p != nil {
			if pid, ok := p["pid"].(float64); ok {
				do(agent, "app.kill", "DELETE", fmt.Sprintf("/v1/workspaces/%s/processes/%d", ws, int(pid)), nil)
			}
		}
		sleep(end, time.Duration(1+rand.IntN(3))*time.Second)
	}
}

func viewer(end time.Time) {
	for k := 0; time.Now().Before(end); k++ {
		d := time.Duration(5+rand.IntN(15)) * time.Second
		gate.RLock()
		vt := doLocked(admin, "viewer.token", "POST", "/v1/viewer-tokens", map[string]any{"workspace": ws, "control": k%2 == 0})
		if vt == nil {
			gate.RUnlock()
			sleep(end, 2*time.Second)
			continue
		}
		tok, _ := vt["token"].(string)
		if k%3 == 2 {
			jpegView(tok, d)
		} else {
			wsView(tok, d, k%2 == 0)
		}
		gate.RUnlock()
		sleep(end, time.Duration(1+rand.IntN(4))*time.Second)
	}
}

func wsView(tok string, d time.Duration, interact bool) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	count(&ops, "viewer.ws")
	c, _, err := websocket.Dial(ctx, "ws://"+apiAddr+"/v1/workspaces/"+ws+"/stream/ws?token="+tok, nil)
	if err != nil {
		count(&errs, "viewer.ws")
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 20)
	if interact {
		go func() {
			for i := 0; ctx.Err() == nil; i++ {
				c.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"t":"move","x":%d,"y":%d}`, 200+i%600, 200+i%400)))
				time.Sleep(150 * time.Millisecond)
			}
		}()
	}
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
	}
}

func jpegView(tok string, d time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	count(&ops, "viewer.jpeg")
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+apiAddr+"/v1/workspaces/"+ws+"/stream/jpeg?token="+tok, nil)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		count(&errs, "viewer.jpeg")
		return
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
}

func lifecycle(cycle time.Duration, end time.Time) {
	for k := 0; sleep(end, cycle/2); k++ {
		op := "freeze"
		if k%2 == 1 {
			op = "suspend"
		}
		gate.Lock()
		doLocked(admin, "ws."+op, "POST", "/v1/workspaces/"+ws+"/"+op, nil)
		time.Sleep(10 * time.Second)
		back := map[string]string{"freeze": "thaw", "suspend": "resume"}[op]
		doLocked(admin, "ws."+back, "POST", "/v1/workspaces/"+ws+"/"+back, nil)
		gate.Unlock()
	}
}

func pidOf(pattern string) int {
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	p, _ := strconv.Atoi(strings.Fields(string(out) + " 0")[0])
	return p
}

func countFDs(pid int) float64 {
	e, _ := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	return float64(len(e))
}

func xResources(display string, xvfb int) (clients, resources float64) {
	cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", xvfb))
	args := strings.Split(string(cmd), "\x00")
	for i := range args {
		if args[i] == "-auth" && i+1 < len(args) {
			os.Setenv("XAUTHORITY", args[i+1])
		}
	}
	c, err := xgb.NewConnDisplay(display)
	if err != nil {
		return -1, -1
	}
	defer c.Close()
	if res.Init(c) != nil {
		return -1, -1
	}
	r, err := res.QueryClients(c).Reply()
	if err != nil {
		return -1, -1
	}
	for _, cl := range r.Clients {
		rr, err := res.QueryClientResources(c, cl.ResourceBase).Reply()
		if err != nil {
			continue
		}
		for _, t := range rr.Types {
			resources += float64(t.Count)
		}
	}
	return float64(len(r.Clients) - 1), resources
}

func collect() map[string]float64 {
	gate.RLock()
	defer gate.RUnlock()
	v := map[string]float64{}
	pid := daemon.Process.Pid
	if u, err := core.ReadUsage(pid); err == nil {
		v["daemon_rss_mb"] = float64(u.RSSKB) / 1024
	}
	v["daemon_fds"] = countFDs(pid)
	var sys map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.Do(ctx, "GET", "/v1/system", nil, &sys); err == nil {
		v["goroutines"], _ = sys["goroutines"].(float64)
		v["daemon_heap_mb"], _ = sys["heap_mb"].(float64)
	}
	var w map[string]any
	if err := admin.Do(ctx, "GET", "/v1/workspaces/"+ws, nil, &w); err != nil {
		return v
	}
	display, _ := w["display"].(string)
	xvfb := 0
	if display != "" {
		xvfb = pidOf("Xvfb " + display + " ")
	}
	var chrome float64
	for _, p := range core.Descendants([]int{pid}) {
		if p == pid || p == xvfb {
			continue
		}
		if u, err := core.ReadUsage(p); err == nil && strings.Contains(u.Comm, "chrom") {
			chrome += float64(u.PSSKB) / 1024
		}
	}
	v["chrome_pss_mb"] = chrome
	if xvfb > 0 {
		if u, err := core.ReadUsage(xvfb); err == nil {
			v["xvfb_rss_mb"] = float64(u.RSSKB) / 1024
		}
		v["x_clients"], v["x_resources"] = xResources(display, xvfb)
	}
	return v
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func spark(xs []float64) string {
	if len(xs) == 0 {
		return ""
	}
	const bars = "▁▂▃▄▅▆▇█"
	step := max(1, len(xs)/60)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, x := range xs {
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	var b strings.Builder
	r := []rune(bars)
	for i := 0; i < len(xs); i += step {
		k := 0
		if hi > lo {
			k = int((xs[i] - lo) / (hi - lo) * float64(len(r)-1))
		}
		b.WriteRune(r[k])
	}
	return b.String()
}

type verdict struct {
	m                metric
	first, mid, last float64
	lo, hi           float64
	growth           float64
	leak             bool
	series           []float64
}

func analyze(samples []sample, warmup time.Duration) []verdict {
	var steady []sample
	for _, s := range samples {
		if s.t >= warmup {
			steady = append(steady, s)
		}
	}
	var out []verdict
	for _, m := range metrics {
		var xs []float64
		for _, s := range steady {
			if x, ok := s.v[m.name]; ok && x >= 0 {
				xs = append(xs, x)
			}
		}
		v := verdict{m: m, series: xs}
		if len(xs) >= 6 {
			n := len(xs) / 3
			v.first, v.mid, v.last = median(xs[:n]), median(xs[n:2*n]), median(xs[2*n:])
			v.growth = v.last - v.first
			v.lo, v.hi = slices(xs)
			tol := math.Max(m.absTol, m.relTol*v.first)
			v.leak = v.first < v.mid && v.mid < v.last && v.growth > tol
		}
		out = append(out, v)
	}
	return out
}

func slices(xs []float64) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, x := range xs {
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	return
}

func main() {
	bin := flag.String("agora", "", "agora binary to soak (required)")
	duration := flag.Duration("duration", 2*time.Hour, "soak duration")
	every := flag.Duration("sample", 30*time.Second, "sampling interval")
	cursors := flag.Int("cursors", 3, "browsing cursors")
	cycle := flag.Duration("cycle", 8*time.Minute, "freeze/thaw and suspend/resume period (alternating)")
	warmup := flag.Duration("warmup", 0, "samples ignored by the leak check (default: 10% of duration, at least 3m)")
	real := flag.Bool("real", true, "also browse real internet pages")
	out := flag.String("out", "bench/results", "output directory")
	flag.Parse()
	if *bin == "" {
		log.Fatal("-agora is required")
	}
	if *warmup == 0 {
		*warmup = max(3*time.Minute, *duration/10)
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		n := rand.IntN(1000)
		fmt.Fprintf(w, localPage, n, n)
	}))
	defer site.Close()
	home, _ := os.MkdirTemp("/tmp", "agora-soak-")
	os.Chmod(home, 0711)
	defer os.RemoveAll(home)
	apiAddr = fmt.Sprintf("127.0.0.1:%d", freePort())
	daemon = exec.Command(*bin, "serve", "--home", home, "--listen", apiAddr)
	logf, _ := os.Create(filepath.Join(home, "daemon.log"))
	daemon.Stdout, daemon.Stderr = logf, logf
	daemon.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := daemon.Start(); err != nil {
		log.Fatal(err)
	}
	defer func() {
		syscall.Kill(-daemon.Process.Pid, syscall.SIGINT)
		daemon.Wait()
	}()
	for i := 0; i < 200; i++ {
		if r, err := http.Get("http://" + apiAddr + "/v1/health"); err == nil {
			r.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	tok, err := os.ReadFile(filepath.Join(home, "admin.token"))
	if err != nil {
		log.Fatal(err)
	}
	admin = &client.Client{URL: "http://" + apiAddr, Token: strings.TrimSpace(string(tok)), HTTP: &http.Client{Timeout: 2 * time.Minute}}
	w := doLocked(admin, "ws.create", "POST", "/v1/workspaces", map[string]any{"name": "soak", "start": true})
	if w == nil {
		log.Fatal("cannot create workspace")
	}
	ws = w["id"].(string)
	start := time.Now()
	end := start.Add(*duration)
	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	for i := 0; i < *cursors+1; i++ {
		cr := doLocked(admin, "cursor.create", "POST", "/v1/cursors", map[string]any{"name": fmt.Sprintf("soak%d", i), "workspace": ws})
		if cr == nil {
			log.Fatal("cannot create cursor")
		}
		agent := &client.Client{URL: admin.URL, Token: cr["token"].(string), HTTP: admin.HTTP}
		if i == *cursors {
			run(func() { desktop(agent, end) })
		} else {
			run(func() { browse(i, agent, site.URL, *real, end) })
		}
	}
	run(func() { viewer(end) })
	run(func() { lifecycle(*cycle, end) })
	var samples []sample
	stamp := time.Now().Format("2006-01-02")
	os.MkdirAll(*out, 0755)
	csvPath := filepath.Join(*out, "soak-"+stamp+".csv")
	cf, _ := os.Create(csvPath)
	cw := csv.NewWriter(cf)
	head := []string{"seconds"}
	for _, m := range metrics {
		head = append(head, m.name)
	}
	cw.Write(head)
	for time.Now().Before(end) {
		v := collect()
		s := sample{time.Since(start), v}
		samples = append(samples, s)
		row := []string{fmt.Sprintf("%.0f", s.t.Seconds())}
		for _, m := range metrics {
			row = append(row, fmt.Sprintf("%.2f", v[m.name]))
		}
		cw.Write(row)
		cw.Flush()
		log.Printf("sample %v %v", s.t.Round(time.Second), v)
		time.Sleep(*every)
	}
	wg.Wait()
	time.Sleep(*every)
	final := collect()
	cf.Close()
	verdicts := analyze(samples, *warmup)
	failed := false
	var md strings.Builder
	host, _ := os.Hostname()
	fmt.Fprintf(&md, "# Soak test %s\n\nHost %s, %d CPUs, binary `%s`, encoder preference %q. Duration %v, %d browsing cursors (real pages: %v), 1 desktop cursor (xclock/xterm/xeyes/xlogo, mouse, keyboard, window move/close, process kill), 1 viewer cycling WebSocket (interactive and observation) and JPEG streams, freeze/thaw and suspend/resume alternating every %v. Samples every %v; the first %v are warm-up and excluded from the leak check.\n\n", stamp, host, numCPU(), *bin, os.Getenv("AGORA_ENCODER"), *duration, *cursors, *real, *cycle/2, *every, *warmup)
	fmt.Fprintf(&md, "A metric fails when the medians of the first, middle and last thirds of the steady-state samples increase monotonically and the total growth exceeds max(absolute, relative × first third).\n\n| Metric | First third | Middle | Last third | Min | Max | Growth | Tolerance | Verdict | Trend |\n|---|---:|---:|---:|---:|---:|---:|---:|---|---|\n")
	for _, v := range verdicts {
		st := "ok"
		if v.leak {
			st = "**GROWTH**"
			failed = true
		}
		if len(v.series) < 6 {
			st = "not enough samples"
		}
		fmt.Fprintf(&md, "| %s %s | %.1f | %.1f | %.1f | %.1f | %.1f | %+.1f | %.0f / %.0f%% | %s | `%s` |\n", v.m.name, v.m.unit, v.first, v.mid, v.last, v.lo, v.hi, v.growth, v.m.absTol, v.m.relTol*100, st, spark(v.series))
	}
	fmt.Fprintf(&md, "\nAfter all actors stopped: daemon RSS %.1f MB, heap %.1f MB, goroutines %.0f, FDs %.0f, X clients %.0f, X resources %.0f.\n\n", final["daemon_rss_mb"], final["daemon_heap_mb"], final["goroutines"], final["daemon_fds"], final["x_clients"], final["x_resources"])
	fmt.Fprintf(&md, "| Operation | Calls | Errors |\n|---|---:|---:|\n")
	var names []string
	ops.Range(func(k, _ any) bool { names = append(names, k.(string)); return true })
	sort.Strings(names)
	for _, n := range names {
		c, _ := ops.Load(n)
		e := int64(0)
		if x, ok := errs.Load(n); ok {
			e = x.(*atomic.Int64).Load()
		}
		fmt.Fprintf(&md, "| %s | %d | %d |\n", n, c.(*atomic.Int64).Load(), e)
	}
	fmt.Fprintf(&md, "\nRaw samples: `%s`.\n", filepath.Base(csvPath))
	mdPath := filepath.Join(*out, "soak-"+stamp+".md")
	os.WriteFile(mdPath, []byte(md.String()), 0644)
	fmt.Println(md.String())
	if failed {
		log.Printf("FAIL: monotonic growth detected, see %s", mdPath)
		syscall.Kill(-daemon.Process.Pid, syscall.SIGINT)
		daemon.Wait()
		os.Exit(1)
	}
}

func numCPU() int {
	b, _ := os.ReadFile("/proc/cpuinfo")
	return strings.Count(string(b), "processor\t:")
}
