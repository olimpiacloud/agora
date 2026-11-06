package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agusgarcia3007/carbon/internal/client"
	"github.com/agusgarcia3007/carbon/internal/core"
	"github.com/coder/websocket"
)

type Row struct {
	Scenario string  `json:"scenario"`
	Metric   string  `json:"metric"`
	Carbon   float64 `json:"carbon"`
	Baseline float64 `json:"baseline"`
	Unit     string  `json:"unit"`
	Note     string  `json:"note,omitempty"`
}

var rows []Row

func add(s, m string, c, b float64, unit, note string) {
	rows = append(rows, Row{s, m, c, b, unit, note})
	log.Printf("%-28s %-34s carbon=%-10.2f baseline=%-10.2f %s %s", s, m, c, b, unit, note)
}

type tree []int

func (t tree) all() []int { return core.Descendants(t) }

func (t tree) pssMB() float64 {
	var s float64
	for _, p := range t.all() {
		u, err := core.ReadUsage(p)
		if err == nil {
			s += float64(u.PSSKB) / 1024
		}
	}
	return s
}

func (t tree) ticks() uint64 {
	var s uint64
	for _, p := range t.all() {
		u, err := core.ReadUsage(p)
		if err == nil {
			s += u.CPUTicks
		}
	}
	return s
}

func (t tree) cpu(d time.Duration) float64 {
	a := t.ticks()
	time.Sleep(d)
	b := t.ticks()
	return float64(b-a) / 100 / d.Seconds() * 100
}

var (
	uid, gid int
	home     string
	carbonC  *client.Client
	daemon   *exec.Cmd
	site     *httptest.Server
	window   = 10 * time.Second
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func startCarbon(bin string) {
	home, _ = os.MkdirTemp("/tmp", "carbon-bench-")
	os.Chmod(home, 0711)
	daemon = exec.Command(bin, "serve", "--home", home, "--listen", "127.0.0.1:7791")
	daemon.Stdout, daemon.Stderr = os.Stderr, os.Stderr
	daemon.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	must(daemon.Start())
	for i := 0; i < 100; i++ {
		if r, err := http.Get("http://127.0.0.1:7791/v1/health"); err == nil {
			r.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	tok, _ := os.ReadFile(filepath.Join(home, "admin.token"))
	carbonC = &client.Client{URL: "http://127.0.0.1:7791", Token: strings.TrimSpace(string(tok)), HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

func cdo(method, path string, body any) map[string]any {
	var out map[string]any
	must(carbonC.Do(context.Background(), method, path, body, &out))
	return out
}

type stack struct {
	procs   []*exec.Cmd
	display int
	vncPort int
	wsPort  int
	cdpPort int
	dir     string
}

func (s *stack) roots() tree {
	t := tree{}
	for _, p := range s.procs {
		if p.Process != nil {
			t = append(t, p.Process.Pid)
		}
	}
	return t
}

func freePort() int {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func (s *stack) run(cred bool, env []string, name string, args ...string) {
	c := exec.Command(name, args...)
	c.Env = append(os.Environ(), env...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cred && uid != 0 {
		c.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	}
	must(c.Start())
	s.procs = append(s.procs, c)
}

func waitTCP(port int, d time.Duration) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log.Fatalf("port %d not ready", port)
}

var nextDisplay = 300

func startBaseline(url string) *stack {
	s := &stack{display: nextDisplay, vncPort: freePort(), wsPort: freePort(), cdpPort: freePort()}
	nextDisplay++
	s.dir, _ = os.MkdirTemp("/tmp", "baseline-")
	os.Chown(s.dir, uid, gid)
	disp := fmt.Sprintf(":%d", s.display)
	s.run(false, nil, "Xvfb", disp, "-screen", "0", "1280x800x24", "-nolisten", "tcp", "-ac")
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(fmt.Sprintf("/tmp/.X11-unix/X%d", s.display)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	env := []string{"DISPLAY=" + disp, "HOME=" + s.dir, "DBUS_SESSION_BUS_ADDRESS=disabled:"}
	if _, err := exec.LookPath("openbox"); err == nil {
		s.run(true, env, "openbox")
	}
	s.run(true, env, "google-chrome", "--user-data-dir="+s.dir+"/profile", "--no-first-run", "--no-default-browser-check", "--password-store=basic",
		"--disable-background-networking", "--disable-sync", "--disable-features=Translate,MediaRouter,OptimizationHints,AutofillServerCommunication",
		"--metrics-recording-only", "--disable-crash-reporter", "--disable-breakpad", "--window-size=1280,800", "--window-position=0,0",
		fmt.Sprintf("--remote-debugging-port=%d", s.cdpPort), "--remote-debugging-address=127.0.0.1", url)
	s.run(false, nil, "x11vnc", "-display", disp, "-localhost", "-rfbport", strconv.Itoa(s.vncPort), "-nopw", "-forever", "-shared", "-noxdamage", "-quiet")
	waitTCP(s.vncPort, 10*time.Second)
	s.run(false, nil, "websockify", fmt.Sprintf("127.0.0.1:%d", s.wsPort), fmt.Sprintf("127.0.0.1:%d", s.vncPort))
	waitTCP(s.wsPort, 10*time.Second)
	waitTCP(s.cdpPort, 20*time.Second)
	return s
}

func (s *stack) pageLoaded(substr string, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", s.cdpPort))
		if err == nil {
			var l []map[string]any
			json.NewDecoder(r.Body).Decode(&l)
			r.Body.Close()
			for _, t := range l {
				if t["type"] == "page" && strings.Contains(fmt.Sprint(t["url"]), substr) && !strings.Contains(fmt.Sprint(t["title"]), "/") {
					return true
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func (s *stack) stop() {
	for i := len(s.procs) - 1; i >= 0; i-- {
		if p := s.procs[i].Process; p != nil {
			syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
	}
	for _, p := range s.procs {
		p.Wait()
	}
	os.RemoveAll(s.dir)
}

func rfbConsume(port int, d time.Duration) (frames int, bytes int64) {
	ctx, cancel := context.WithTimeout(context.Background(), d+5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/", port), &websocket.DialOptions{Subprotocols: []string{"binary"}})
	must(err)
	ws.SetReadLimit(64 << 20)
	c := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer c.Close()
	buf := make([]byte, 12)
	io.ReadFull(c, buf)
	c.Write([]byte("RFB 003.008\n"))
	n := make([]byte, 1)
	io.ReadFull(c, n)
	types := make([]byte, n[0])
	io.ReadFull(c, types)
	c.Write([]byte{1})
	res := make([]byte, 4)
	io.ReadFull(c, res)
	c.Write([]byte{1})
	si := make([]byte, 24)
	io.ReadFull(c, si)
	w, h := binary.BigEndian.Uint16(si), binary.BigEndian.Uint16(si[2:])
	bpp := int(si[4]) / 8
	nl := binary.BigEndian.Uint32(si[20:])
	io.CopyN(io.Discard, c, int64(nl))
	c.Write([]byte{2, 0, 0, 1, 0, 0, 0, 0})
	req := func(inc byte) {
		b := []byte{3, inc, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint16(b[6:], w)
		binary.BigEndian.PutUint16(b[8:], h)
		c.Write(b)
	}
	req(0)
	end := time.Now().Add(d)
	hdr := make([]byte, 4)
	rh := make([]byte, 12)
	for time.Now().Before(end) {
		c.SetReadDeadline(end)
		if _, err := io.ReadFull(c, hdr[:1]); err != nil {
			break
		}
		switch hdr[0] {
		case 0:
			io.ReadFull(c, hdr[1:4])
			nr := int(binary.BigEndian.Uint16(hdr[2:]))
			for i := 0; i < nr; i++ {
				io.ReadFull(c, rh)
				rw, rhh := int(binary.BigEndian.Uint16(rh[4:])), int(binary.BigEndian.Uint16(rh[6:]))
				enc := int32(binary.BigEndian.Uint32(rh[8:]))
				if enc == 0 {
					k, _ := io.CopyN(io.Discard, c, int64(rw*rhh*bpp))
					bytes += k
				}
			}
			bytes += 4 + int64(nr)*12
			frames++
			req(1)
		case 2:
		case 3:
			io.ReadFull(c, hdr[1:4])
			l := make([]byte, 4)
			io.ReadFull(c, l)
			io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint32(l)))
		}
	}
	return
}

func carbonConsume(ws string, d time.Duration) (frames int, bytes int64) {
	vt := cdo("POST", "/v1/viewer-tokens", map[string]any{"workspace": ws, "control": false})["token"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), d+5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://127.0.0.1:7791/v1/workspaces/"+ws+"/stream/ws?token="+vt, nil)
	must(err)
	c.SetReadLimit(64 << 20)
	defer c.CloseNow()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		rc, cc := context.WithDeadline(ctx, end)
		typ, b, err := c.Read(rc)
		cc()
		if err != nil {
			break
		}
		if typ == websocket.MessageBinary {
			frames++
			bytes += int64(len(b))
		}
	}
	return
}

func xvfbOf(ws string) int {
	v := cdo("GET", "/v1/workspaces/"+ws, nil)
	d, _ := v["display"].(string)
	out, _ := exec.Command("pgrep", "-f", "Xvfb "+d+" ").Output()
	p, _ := strconv.Atoi(strings.Fields(string(out) + " 0")[0])
	return p
}

func carbonTree() tree {
	return tree{daemon.Process.Pid}
}

const animPage = "data:text/html,<body style='margin:0;font:40px sans-serif'><div id=d></div><canvas id=c width=600 height=300></canvas><script>const x=c.getContext('2d');let i=0;setInterval(()=>{d.textContent=Date.now();x.fillStyle='hsl('+(i++%360)+',70%,50%)';x.fillRect((i*7)%560,(i*3)%260,40,40)},33)</script>"

func main() {
	bin := flag.String("carbon", "./bin/carbon", "carbon binary")
	out := flag.String("out", "bench/results", "output directory")
	quick := flag.Bool("quick", false, "shorter windows")
	flag.Parse()
	if *quick {
		window = 5 * time.Second
	}
	if os.Getuid() == 0 {
		u, err := user.Lookup("carbon")
		must(err)
		uid, _ = strconv.Atoi(u.Uid)
		gid, _ = strconv.Atoi(u.Gid)
	}
	site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<title>bench</title><h1 id=h>bench page</h1><input id=i><script>document.addEventListener('mousedown',e=>{window.__md=performance.timeOrigin+performance.now()},true)</script>")
	}))
	defer site.Close()
	startCarbon(*bin)
	defer func() {
		syscall.Kill(-daemon.Process.Pid, syscall.SIGINT)
		daemon.Wait()
		os.RemoveAll(home)
	}()
	time.Sleep(time.Second)

	add("1 idle daemon", "memory PSS", carbonTree().pssMB(), 0, "MB", "baseline has no daemon")
	add("1 idle daemon", "CPU", carbonTree().cpu(window), 0, "%", "")

	t0 := time.Now()
	w := cdo("POST", "/v1/workspaces", map[string]any{"name": "empty", "start": true})
	wsEmpty := w["id"].(string)
	carbonEmpty := time.Since(t0)
	time.Sleep(time.Second)
	cm := carbonTree().pssMB()
	b := startBaselineNoChrome()
	time.Sleep(time.Second)
	add("2 empty desktop", "memory PSS", cm, b.roots().pssMB(), "MB", "carbon: daemon+Xvfb+WM; baseline: Xvfb+openbox+x11vnc+websockify")
	add("2 empty desktop", "CPU idle", carbonTree().cpu(window), b.roots().cpu(window), "%", "")
	add("2 empty desktop", "start time", float64(carbonEmpty.Milliseconds()), float64(b.startMs), "ms", "")
	b.stop()
	cdo("DELETE", "/v1/workspaces/"+wsEmpty+"?purge=1", nil)

	t0 = time.Now()
	w = cdo("POST", "/v1/workspaces", map[string]any{"name": "chrome", "start": true})
	ws := w["id"].(string)
	cr := cdo("POST", "/v1/cursors", map[string]any{"name": "a0", "workspace": ws})
	agent := &client.Client{URL: carbonC.URL, Token: cr["token"].(string), HTTP: carbonC.HTTP}
	var tab map[string]any
	must(agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs", map[string]any{"url": site.URL}, &tab))
	target := tab["target"].(string)
	for {
		var o map[string]any
		agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "eval", "expression": "document.readyState"}, &o)
		if o["value"] == "complete" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	carbonStart := time.Since(t0)
	t0 = time.Now()
	bs := startBaseline(site.URL)
	bs.pageLoaded(strings.TrimPrefix(site.URL, "http://"), 30*time.Second)
	baseStart := time.Since(t0)
	time.Sleep(3 * time.Second)
	add("3 desktop + Chromium", "cold start to page loaded", float64(carbonStart.Milliseconds()), float64(baseStart.Milliseconds()), "ms", "")
	add("3 desktop + Chromium", "memory PSS", carbonTree().pssMB(), bs.roots().pssMB(), "MB", "Chromium included on both sides")
	add("3 desktop + Chromium", "CPU idle, no viewer", carbonTree().cpu(window), bs.roots().cpu(window), "%", "")

	lat := func(f func()) float64 {
		var ds []float64
		for i := 0; i < 15; i++ {
			t := time.Now()
			f()
			ds = append(ds, float64(time.Since(t).Microseconds())/1000)
		}
		sort.Float64s(ds)
		return ds[len(ds)/2]
	}
	shot := lat(func() {
		_, _, err := carbonC.Raw(context.Background(), "GET", "/v1/workspaces/"+ws+"/screenshot?format=jpeg", nil)
		must(err)
	})
	bcdp := newCDP(bs.cdpPort)
	bshot := lat(func() { bcdp.call("Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": 75}) })
	add("4 agent capture", "full screenshot latency p50", shot, bshot, "ms", "carbon: X shm grab of whole desktop + JPEG; baseline: CDP Page.captureScreenshot")
	tshot := lat(func() {
		var o map[string]any
		must(agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "screenshot"}, &o))
	})
	add("4 agent capture", "tab screenshot via API p50", tshot, bshot, "ms", "carbon adds HTTP+auth+arbitration over raw CDP")
	semantic := lat(func() {
		var o map[string]any
		must(agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "snapshot"}, &o))
	})
	add("4 agent capture", "semantic snapshot p50", semantic, 0, "ms", "no baseline equivalent; replaces screenshots for agents")

	inLat := func(do func()) float64 {
		var ds []float64
		for i := 0; i < 10; i++ {
			agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "eval", "expression": "window.__md=0"}, nil)
			t := float64(time.Now().UnixNano()) / 1e6
			do()
			for k := 0; k < 200; k++ {
				var o map[string]any
				agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "eval", "expression": "window.__md"}, &o)
				if v, _ := o["value"].(float64); v > 0 {
					ds = append(ds, v-t)
					break
				}
			}
		}
		sort.Float64s(ds)
		if len(ds) == 0 {
			return -1
		}
		return ds[len(ds)/2]
	}
	wins := []map[string]any{}
	must(carbonC.Do(context.Background(), "GET", "/v1/workspaces/"+ws+"/windows", nil, &wins))
	cx, cy := 640.0, 500.0
	if len(wins) > 0 {
		cx = wins[0]["x"].(float64) + wins[0]["width"].(float64)/2
		cy = wins[0]["y"].(float64) + wins[0]["height"].(float64)*0.7
	}
	seat := inLat(func() {
		must(agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "click", "x": cx, "y": cy}, nil))
	})
	cdpClick := inLat(func() {
		must(agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "click", "x": 300, "y": 300}, nil))
	})
	xdo := inLat(func() {
		c := exec.Command("xdotool", "mousemove", fmt.Sprint(int(cx)), fmt.Sprint(int(cy)), "click", "1")
		v := cdo("GET", "/v1/workspaces/"+ws, nil)
		c.Env = append(os.Environ(), "DISPLAY="+v["display"].(string), "XAUTHORITY="+filepath.Join(home, "workspaces", ws, "run", "xauth-"+strings.TrimPrefix(v["display"].(string), ":")))
		c.Run()
	})
	add("5 input", "click → DOM mousedown p50 (X11 seat)", seat, xdo, "ms", "carbon: API→own MPX seat via XTEST; baseline: xdotool process")
	add("5 input", "click → DOM mousedown p50 (CDP)", cdpClick, 0, "ms", "carbon semantic click through API")

	t0 = time.Now()
	var subTabs []string
	for i := 1; i < 5; i++ {
		r := cdo("POST", "/v1/cursors", map[string]any{"name": fmt.Sprintf("a%d", i), "workspace": ws})
		ac := &client.Client{URL: carbonC.URL, Token: r["token"].(string), HTTP: carbonC.HTTP}
		var tb map[string]any
		must(ac.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs", map[string]any{"url": site.URL}, &tb))
		subTabs = append(subTabs, tb["target"].(string))
	}
	perCursor := time.Since(t0) / 4
	t0 = time.Now()
	var extra []*stack
	for i := 1; i < 5; i++ {
		s := startBaseline(site.URL)
		s.pageLoaded(strings.TrimPrefix(site.URL, "http://"), 30*time.Second)
		extra = append(extra, s)
	}
	perStack := time.Since(t0) / 4
	time.Sleep(4 * time.Second)
	allBase := append(tree{}, bs.roots()...)
	for _, s := range extra {
		allBase = append(allBase, s.roots()...)
	}
	add("6 five agents with a browser", "new agent ready (cursor+window vs new stack)", float64(perCursor.Milliseconds()), float64(perStack.Milliseconds()), "ms", "")
	add("6 five agents with a browser", "memory PSS", carbonTree().pssMB(), allBase.pssMB(), "MB", "carbon: 1 desktop, 1 Chromium, 5 windows; baseline: 5 full stacks")
	add("6 five agents with a browser", "CPU idle, no viewer", carbonTree().cpu(window), allBase.cpu(window), "%", "")
	for _, s := range extra {
		s.stop()
	}

	before := carbonTree().pssMB()
	var ids []string
	for i := 0; i < 100; i++ {
		r := cdo("POST", "/v1/cursors", map[string]any{"name": fmt.Sprintf("idle-%d", i), "workspace": ws})
		ids = append(ids, r["cursor"].(map[string]any)["id"].(string))
	}
	time.Sleep(time.Second)
	after := carbonTree().pssMB()
	add("7 idle cursors", "memory per idle cursor", (after-before)*1024/100, 0, "KB", "100 logical cursors without graphics; baseline would need a stack each")
	for _, id := range ids {
		cdo("DELETE", "/v1/cursors/"+id, nil)
	}

	sv := cdo("POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "navigate", "url": animPage})
	_ = sv
	bcdp.call("Page.navigate", map[string]any{"url": animPage})
	time.Sleep(2 * time.Second)
	add("8 animated page, no viewer", "CPU", carbonTree().cpu(window), bs.roots().cpu(window), "%", "page animation cost on both; carbon encodes nothing")
	stopC := make(chan [2]float64)
	go func() {
		f, by := carbonConsume(ws, window)
		stopC <- [2]float64{float64(f), float64(by)}
	}()
	time.Sleep(time.Second)
	ccpu := carbonTree().cpu(window - 2*time.Second)
	cres := <-stopC
	go func() {
		f, by := rfbConsume(bs.wsPort, window)
		stopC <- [2]float64{float64(f), float64(by)}
	}()
	time.Sleep(time.Second)
	bcpu := bs.roots().cpu(window - 2*time.Second)
	bres := <-stopC
	add("9 streaming, 1 viewer", "server CPU (desktop+browser+stream)", ccpu, bcpu, "%", "baseline client requests raw RFB through websockify (cheapest server encoding)")
	add("9 streaming, 1 viewer", "frames/s delivered", cres[0]/window.Seconds(), bres[0]/window.Seconds(), "fps", "")
	add("9 streaming, 1 viewer", "bandwidth", cres[1]*8/1000/window.Seconds(), bres[1]*8/1000/window.Seconds(), "kbit/s", "H.264 vs raw RFB")
	multi := func(n int, f func() [2]float64) [2]float64 {
		ch := make(chan [2]float64, n)
		for i := 0; i < n; i++ {
			go func() { ch <- f() }()
		}
		var s [2]float64
		for i := 0; i < n; i++ {
			r := <-ch
			s[0] += r[0]
			s[1] += r[1]
		}
		return s
	}
	var ccpu3, bcpu3 float64
	done := make(chan bool)
	go func() {
		multi(3, func() [2]float64 { f, b := carbonConsume(ws, window); return [2]float64{float64(f), float64(b)} })
		done <- true
	}()
	time.Sleep(time.Second)
	ccpu3 = carbonTree().cpu(window - 2*time.Second)
	<-done
	go func() {
		multi(3, func() [2]float64 { f, b := rfbConsume(bs.wsPort, window); return [2]float64{float64(f), float64(b)} })
		done <- true
	}()
	time.Sleep(time.Second)
	bcpu3 = bs.roots().cpu(window - 2*time.Second)
	<-done
	add("10 streaming, 3 viewers", "server CPU", ccpu3, bcpu3, "%", "carbon encodes once and fans out")

	cdo("POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "navigate", "url": site.URL})
	time.Sleep(time.Second)
	t0 = time.Now()
	cdo("POST", "/v1/workspaces/"+ws+"/suspend", nil)
	suspendMs := time.Since(t0).Milliseconds()
	time.Sleep(time.Second)
	add("11 suspended workspace", "memory PSS (whole carbon)", carbonTree().pssMB(), bs.roots().pssMB(), "MB", "baseline cannot suspend without losing the live session")
	add("11 suspended workspace", "suspend time", float64(suspendMs), 0, "ms", "checkpoint + graceful browser close")
	t0 = time.Now()
	cdo("POST", "/v1/workspaces/"+ws+"/resume", nil)
	for k := 0; k < 400; k++ {
		var tabs []map[string]any
		carbonC.Do(context.Background(), "GET", "/v1/workspaces/"+ws+"/tabs", nil, &tabs)
		ok := 0
		for _, t := range tabs {
			if t["title"] == "bench" {
				ok++
			}
		}
		if ok >= 5 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	add("11 suspended workspace", "resume until 5 tabs restored", float64(time.Since(t0).Milliseconds()), float64(baseStart.Milliseconds()), "ms", "baseline: cold start of one stack")
	t0 = time.Now()
	cdo("DELETE", "/v1/workspaces/"+ws+"?purge=1", nil)
	add("12 teardown", "destroy workspace", float64(time.Since(t0).Milliseconds()), 0, "ms", "")
	bs.stop()

	os.MkdirAll(*out, 0755)
	stamp := time.Now().Format("2006-01-02T15-04")
	jb, _ := json.MarshalIndent(map[string]any{"time": time.Now(), "host": hostInfo(), "rows": rows}, "", "  ")
	os.WriteFile(filepath.Join(*out, stamp+".json"), jb, 0644)
	var md strings.Builder
	fmt.Fprintf(&md, "# Carbon vs Xvfb+x11vnc+noVNC+Chrome\n\n%s\n\nWindow per CPU sample: %v. PSS sums every process of each stack (Chromium included).\n\n| Scenario | Metric | Carbon | Baseline | Unit | Notes |\n|---|---|---:|---:|---|---|\n", hostInfo(), window)
	for _, r := range rows {
		b := fmt.Sprintf("%.1f", r.Baseline)
		if r.Baseline == 0 {
			b = "—"
		}
		fmt.Fprintf(&md, "| %s | %s | %.1f | %s | %s | %s |\n", r.Scenario, r.Metric, r.Carbon, b, r.Unit, r.Note)
	}
	os.WriteFile(filepath.Join(*out, stamp+".md"), []byte(md.String()), 0644)
	os.WriteFile(filepath.Join(*out, "latest.md"), []byte(md.String()), 0644)
	fmt.Println(md.String())
}

type baseNoChrome struct {
	*stack
	startMs int64
}

func startBaselineNoChrome() *baseNoChrome {
	t0 := time.Now()
	s := &stack{display: nextDisplay, vncPort: freePort(), wsPort: freePort()}
	nextDisplay++
	s.dir, _ = os.MkdirTemp("/tmp", "baseline-")
	disp := fmt.Sprintf(":%d", s.display)
	s.run(false, nil, "Xvfb", disp, "-screen", "0", "1280x800x24", "-nolisten", "tcp", "-ac")
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(fmt.Sprintf("/tmp/.X11-unix/X%d", s.display)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := exec.LookPath("openbox"); err == nil {
		s.run(false, []string{"DISPLAY=" + disp}, "openbox")
	}
	s.run(false, nil, "x11vnc", "-display", disp, "-localhost", "-rfbport", strconv.Itoa(s.vncPort), "-nopw", "-forever", "-shared", "-noxdamage", "-quiet")
	waitTCP(s.vncPort, 10*time.Second)
	s.run(false, nil, "websockify", fmt.Sprintf("127.0.0.1:%d", s.wsPort), fmt.Sprintf("127.0.0.1:%d", s.vncPort))
	waitTCP(s.wsPort, 10*time.Second)
	return &baseNoChrome{s, time.Since(t0).Milliseconds()}
}

func hostInfo() string {
	cpu := ""
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "model name") {
				cpu = strings.TrimSpace(strings.SplitN(l, ":", 2)[1])
				break
			}
		}
	}
	ver, _ := exec.Command("google-chrome", "--version").Output()
	return fmt.Sprintf("Host: %s, %d threads; %s; carbon %s", cpu, numCPU(), strings.TrimSpace(string(ver)), core.Version)
}

func numCPU() int {
	b, _ := os.ReadFile("/proc/cpuinfo")
	return strings.Count(string(b), "processor\t:")
}

type cdpConn struct {
	c  *websocket.Conn
	id int
}

func newCDP(port int) *cdpConn {
	r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	must(err)
	var l []map[string]any
	json.NewDecoder(r.Body).Decode(&l)
	r.Body.Close()
	var u string
	for _, t := range l {
		if t["type"] == "page" {
			u = t["webSocketDebuggerUrl"].(string)
			break
		}
	}
	c, _, err := websocket.Dial(context.Background(), u, nil)
	must(err)
	c.SetReadLimit(64 << 20)
	return &cdpConn{c: c}
}

func (c *cdpConn) call(method string, params any) map[string]any {
	c.id++
	b, _ := json.Marshal(map[string]any{"id": c.id, "method": method, "params": params})
	c.c.Write(context.Background(), websocket.MessageText, b)
	for {
		_, d, err := c.c.Read(context.Background())
		must(err)
		var m map[string]any
		json.Unmarshal(d, &m)
		if id, _ := m["id"].(float64); int(id) == c.id {
			return m
		}
	}
}
