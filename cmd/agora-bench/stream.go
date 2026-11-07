package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/olimpiacloud/agora/internal/client"
	"github.com/olimpiacloud/agora/internal/core"
)

const fullHTML = `<title>full</title><body style="margin:0;overflow:hidden"><canvas id=c></canvas><script>c.width=innerWidth;c.height=innerHeight;const x=c.getContext('2d');let i=0;function f(){i++;for(let k=0;k<12;k++){x.fillStyle='hsl('+((i*5+k*30)%360)+',70%,'+(30+k*4)+'%)';x.fillRect(0,k*c.height/12,c.width,c.height/12)}x.fillStyle='#fff';x.font='48px sans-serif';for(let k=0;k<20;k++)x.fillText('frame '+i+' line '+k+' '+Date.now(),((i*9+k*50)%c.width)-200,40+k*55);requestAnimationFrame(f)}f()</script>`

type streamStats struct {
	frames  float64
	encMs   float64
	capMs   float64
	latMs   float64
	encoder string
}

func readStream(ws string) streamStats {
	v := cdo("GET", "/v1/workspaces/"+ws, nil)
	st, _ := v["stream"].(map[string]any)
	f := func(k string) float64 { x, _ := st[k].(float64); return x }
	e, _ := st["encoder"].(string)
	return streamStats{f("frames"), f("encode_ms_avg"), f("capture_ms_avg"), f("latency_ms_avg"), e}
}

func windowAvg(a, b streamStats, get func(streamStats) float64) float64 {
	if b.frames <= a.frames {
		return 0
	}
	return (get(b)*b.frames - get(a)*a.frames) / (b.frames - a.frames)
}

func pidCPU(pid int, d time.Duration) float64 {
	a, _, _ := core.StatLight(pid)
	time.Sleep(d)
	b, _, _ := core.StatLight(pid)
	return float64(b-a) / d.Seconds()
}

type streamResult map[string]float64

func streamScenario(ws string, res streamResult, name string, viewers int, interact bool) {
	type out struct{ f, b float64 }
	ch := make(chan out, viewers)
	for i := 0; i < viewers; i++ {
		go func() {
			f, b := agoraConsumeOpt(ws, window, interact)
			ch <- out{float64(f), float64(b)}
		}()
	}
	time.Sleep(time.Second)
	s0 := readStream(ws)
	d := window - 2*time.Second
	dcpu := make(chan float64)
	go func() { dcpu <- pidCPU(daemon.Process.Pid, d) }()
	tcpu := agoraTree().cpu(d)
	res[name+"|daemon CPU"] = <-dcpu
	res[name+"|tree CPU"] = tcpu
	s1 := readStream(ws)
	var fr, by float64
	for i := 0; i < viewers; i++ {
		o := <-ch
		fr += o.f
		by += o.b
	}
	res[name+"|fps per viewer"] = fr / float64(viewers) / window.Seconds()
	res[name+"|bandwidth per viewer"] = by * 8 / 1000 / float64(viewers) / window.Seconds()
	res[name+"|encode ms/frame"] = windowAvg(s0, s1, func(s streamStats) float64 { return s.encMs })
	res[name+"|capture ms/frame"] = windowAvg(s0, s1, func(s streamStats) float64 { return s.capMs })
	if s1.latMs > 0 {
		res[name+"|glass-to-encode ms"] = windowAvg(s0, s1, func(s streamStats) float64 { return s.latMs })
	}
	log.Printf("%s: encoder=%q %v", name, s1.encoder, res)
}

func changeToFrame(ws string, agent *client.Client, target string, samples int) (median, p95 float64) {
	vt := cdo("POST", "/v1/viewer-tokens", map[string]any{"workspace": ws, "control": false})["token"].(string)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+apiAddr+"/v1/workspaces/"+ws+"/stream/ws?token="+vt, nil)
	must(err)
	c.SetReadLimit(64 << 20)
	defer c.CloseNow()
	arrivals := make(chan time.Time, 1024)
	go func() {
		for {
			typ, _, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				arrivals <- time.Now()
			}
		}
	}()
	var lat []float64
	for i := 0; i < samples; i++ {
		time.Sleep(400 * time.Millisecond)
		for len(arrivals) > 0 {
			<-arrivals
		}
		t0 := time.Now()
		var o map[string]any
		agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "eval", "expression": fmt.Sprintf("document.getElementById('h').textContent='change %d'", i)}, &o)
		select {
		case t := <-arrivals:
			lat = append(lat, float64(t.Sub(t0).Microseconds())/1000)
		case <-time.After(3 * time.Second):
			lat = append(lat, 3000)
		}
	}
	sort.Float64s(lat)
	return lat[len(lat)/2], lat[len(lat)*95/100]
}

func runStreamOnce(bin string) streamResult {
	startAgora(bin)
	defer func() {
		syscall.Kill(-daemon.Process.Pid, syscall.SIGINT)
		daemon.Wait()
		os.RemoveAll(home)
	}()
	res := streamResult{}
	w := cdo("POST", "/v1/workspaces", map[string]any{"name": "stream", "start": true})
	ws := w["id"].(string)
	cr := cdo("POST", "/v1/cursors", map[string]any{"name": "a0", "workspace": ws})
	agent := &client.Client{URL: agoraC.URL, Token: cr["token"].(string), HTTP: agoraC.HTTP}
	var tab map[string]any
	must(agent.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs", map[string]any{"url": site.URL}, &tab))
	target := tab["target"].(string)
	time.Sleep(2 * time.Second)
	med, p95 := changeToFrame(ws, agent, target, 30)
	res["L page change to first frame|median ms"] = med
	res["L page change to first frame|p95 ms"] = p95
	nav := func(path string) {
		cdo("POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "navigate", "url": site.URL + path})
		time.Sleep(2 * time.Second)
	}
	nav("/anim")
	streamScenario(ws, res, "9 streaming, 1 viewer", 1, false)
	streamScenario(ws, res, "9b streaming, viewer interacting", 1, true)
	streamScenario(ws, res, "10 streaming, 3 viewers", 3, false)
	nav("/full")
	streamScenario(ws, res, "9c full-screen animation, 1 viewer", 1, false)
	if s := readStream(ws); s.encoder != "" {
		res["encoder|"+s.encoder] = 1
	}
	cdo("DELETE", "/v1/workspaces/"+ws+"?purge=1", nil)
	return res
}

func streamSuite(after, before string, rounds int, outDir string) {
	sum := map[string]streamResult{"after": {}, "before": {}}
	bins := [][2]string{{"after", after}}
	if before != "" {
		bins = [][2]string{{"before", before}, {"after", after}}
	}
	for r := 0; r < rounds; r++ {
		for _, b := range bins {
			log.Printf("round %d: %s (%s)", r+1, b[0], b[1])
			for k, v := range runStreamOnce(b[1]) {
				sum[b[0]][k] += v / float64(rounds)
			}
		}
	}
	var keys []string
	for k := range sum["after"] {
		keys = append(keys, k)
	}
	for k := range sum["before"] {
		if _, ok := sum["after"][k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var md strings.Builder
	fmt.Fprintf(&md, "# Streaming: after vs before\n\n%s\n\nafter: `%s` (AGORA_ENCODER=%q), before: `%s`. %d alternating rounds per binary, window %v per CPU sample (first and last second excluded). CPU is %% of one core. Other agents share this machine: treat CPU as noisy.\n\n| Scenario | Metric | After | Before |\n|---|---|---:|---:|\n", hostInfo(), after, os.Getenv("AGORA_ENCODER"), before, rounds, window)
	for _, k := range keys {
		sc, m, _ := strings.Cut(k, "|")
		if sc == "encoder" {
			continue
		}
		b := "—"
		if v, ok := sum["before"][k]; ok {
			b = fmt.Sprintf("%.2f", v)
		}
		a := "—"
		if v, ok := sum["after"][k]; ok {
			a = fmt.Sprintf("%.2f", v)
		}
		fmt.Fprintf(&md, "| %s | %s | %s | %s |\n", sc, m, a, b)
	}
	for _, k := range keys {
		if sc, e, _ := strings.Cut(k, "|"); sc == "encoder" {
			fmt.Fprintf(&md, "\nEncoder reported by the after daemon: %s\n", e)
		}
	}
	os.MkdirAll(outDir, 0755)
	name := filepath.Join(outDir, "stream-"+time.Now().Format("2006-01-02T15-04")+".md")
	os.WriteFile(name, []byte(md.String()), 0644)
	fmt.Println(md.String())
}
