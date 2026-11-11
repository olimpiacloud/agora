package e2e

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/olimpiacloud/agora/internal/sandbox"
)

func TestA_SessionPersistence(t *testing.T) {
	h := newH(t)
	ws := h.workspace("session")
	cid, c := h.cursor(h.admin, map[string]any{"name": "login-agent", "workspace": ws})
	_ = cid
	tab := h.tab(c, ws, h.site.URL+"/login")
	snap := h.act(c, ws, tab, map[string]any{"action": "snapshot"})["snapshot"].(string)
	ref := func(name string) string {
		for _, l := range strings.Split(snap, "\n") {
			if strings.Contains(l, fmt.Sprintf("%q", name)) {
				return strings.Trim(strings.Fields(l)[0], "[]")
			}
		}
		t.Fatalf("no element %q in\n%s", name, snap)
		return ""
	}
	h.act(c, ws, tab, map[string]any{"action": "fill", "ref": ref("user"), "text": "agus"})
	h.act(c, ws, tab, map[string]any{"action": "fill", "ref": ref("password"), "text": "hunter2", "submit": true})
	h.waitEval(c, ws, tab, "document.title", "Me:user-agus", 10*time.Second)
	time.Sleep(500 * time.Millisecond)
	ls := h.eval(c, ws, tab, "localStorage.getItem('ls')")
	idb := h.eval(c, ws, tab, "new Promise(r=>{const q=indexedDB.open('agora',1);q.onsuccess=()=>{const g=q.result.transaction('kv').objectStore('kv').get('k');g.onsuccess=()=>r(g.result||null)}})")
	t.Logf("logged in; localStorage=%v indexedDB=%v", ls, idb)

	ev := h.do(h.admin, "GET", "/v1/events?workspace="+ws+"&limit=500", nil)
	if strings.Contains(jsonStr(ev), "hunter2") {
		t.Fatal("password leaked into events")
	}
	logs, _ := os.ReadFile(h.home + "/events/" + ws + ".jsonl")
	if strings.Contains(string(logs), "hunter2") {
		t.Fatal("password leaked into audit log")
	}

	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/browser-restart", nil)
	var tabs []map[string]any
	for i := 0; i < 50; i++ {
		tabs = h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/tabs")
		if len(tabs) > 0 && strings.Contains(fmt.Sprint(tabs), "/me") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("after browser restart tabs=%v", tabs)
	nt := h.tab(c, ws, h.site.URL+"/me")
	h.waitEval(c, ws, nt, "document.title", "Me:user-agus", 10*time.Second)
	h.waitEval(c, ws, nt, "localStorage.getItem('ls')", ls, 5*time.Second)

	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/suspend", nil)
	v := h.wsView(ws)
	if v["state"] != "suspended" || v["browser"] == true || v["display"] != nil {
		t.Fatalf("suspend did not release: %v", v)
	}
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/resume", nil)
	var restored string
	for i := 0; i < 80 && restored == ""; i++ {
		for _, tb := range h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/tabs") {
			if strings.Contains(fmt.Sprint(tb["url"]), "/me") && tb["owner"] == cid {
				restored = tb["target"].(string)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if restored == "" {
		t.Fatal("tab not restored to its owner after resume")
	}
	h.waitEval(c, ws, restored, "document.getElementById('who')&&document.getElementById('who').textContent", "user-agus", 10*time.Second)
	h.waitEval(c, ws, restored, "localStorage.getItem('ls')", ls, 5*time.Second)
	h.waitEval(c, ws, restored, "new Promise(r=>{const q=indexedDB.open('agora',1);q.onsuccess=()=>{const g=q.result.transaction('kv').objectStore('kv').get('k');g.onsuccess=()=>r(g.result||null)}})", idb, 5*time.Second)
	t.Log("PASS A: cookie, localStorage and IndexedDB survive browser restart and suspend/resume; tab restored to owner; no secrets in logs")
}

func TestB_FiveSubagents(t *testing.T) {
	h := newH(t)
	ws := h.workspace("swarm")
	mainID, mainC := h.cursor(h.admin, map[string]any{"name": "main", "workspace": ws, "goal": "coordinate"})
	h.tab(mainC, ws, "about:blank")
	before := countProcs(t, "Xvfb .*"+h.home)
	type res struct {
		id  string
		err error
	}
	ids := make([]string, 5)
	toks := make([]string, 5)
	for i := 0; i < 5; i++ {
		r := h.do(mainC, "POST", "/v1/cursors", map[string]any{"name": fmt.Sprintf("sub-%d", i), "goal": fmt.Sprintf("read secret %d and answer", i)})
		ids[i] = r["cursor"].(map[string]any)["id"].(string)
		toks[i] = r["token"].(string)
		if r["cursor"].(map[string]any)["parent"] != mainID {
			t.Fatalf("parent not set")
		}
	}
	var wg sync.WaitGroup
	t0 := time.Now()
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := h.as(toks[i])
			var tr map[string]any
			if err := c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs", map[string]any{"url": fmt.Sprintf("%s/work?n=%d", h.site.URL, i)}, &tr); err != nil {
				c.Do(context.Background(), "POST", "/v1/cursors/self/fail", map[string]any{"error": err.Error()}, nil)
				return
			}
			tg := tr["target"].(string)
			c.Do(context.Background(), "POST", "/v1/cursors/self/report", map[string]any{"message": "page open", "percent": 30}, nil)
			var secret string
			for k := 0; k < 40 && secret == ""; k++ {
				var o map[string]any
				c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tg, map[string]any{"action": "eval", "expression": "(document.getElementById('secret')||{}).textContent||''"}, &o)
				secret, _ = o["value"].(string)
				time.Sleep(100 * time.Millisecond)
			}
			var o map[string]any
			c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tg, map[string]any{"action": "snapshot"}, &o)
			snap, _ := o["snapshot"].(string)
			ref := ""
			for _, l := range strings.Split(snap, "\n") {
				if strings.Contains(l, `"answer"`) {
					ref = strings.Trim(strings.Fields(l)[0], "[]")
				}
			}
			c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tg, map[string]any{"action": "type", "ref": ref, "text": secret}, nil)
			c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tg, map[string]any{"action": "click", "ref": "e2"}, nil)
			var title map[string]any
			c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tg, map[string]any{"action": "eval", "expression": "document.title"}, &title)
			c.Do(context.Background(), "POST", "/v1/cursors/self/complete", map[string]any{"result": map[string]any{"secret": secret, "title": title["value"]}}, nil)
		}(i)
	}
	w := h.do(mainC, "POST", "/v1/wait", map[string]any{"ids": ids, "timeout_ms": 60000})
	wg.Wait()
	el := time.Since(t0)
	if w["timeout"] == true {
		t.Fatalf("timeout: %v", w)
	}
	for i, cv := range w["cursors"].([]any) {
		c := cv.(map[string]any)
		r := c["result"].(map[string]any)
		if c["status"] != "done" || r["secret"] != fmt.Sprintf("secret-%d", i) || r["title"] != fmt.Sprintf("answered:secret-%d", i) {
			t.Fatalf("sub-%d bad result: %v", i, c)
		}
	}
	after := countProcs(t, "Xvfb .*"+h.home)
	browsers := countProcs(t, "remote-debugging-pipe --user-data-dir="+h.home) - countProcs(t, sandbox.Cmd+" .*remote-debugging-pipe --user-data-dir="+h.home)
	t.Logf("5 subagents done in %v; Xvfb processes before=%d after=%d; browser instances for this home=%d", el, before, after, browsers)
	if after != before || browsers != 1 {
		t.Fatalf("expected no new desktops and one browser, got xvfb %d->%d browsers %d", before, after, browsers)
	}
	tabs := h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/tabs")
	owned := 0
	for _, tb := range tabs {
		if tb["owner"] != "" && tb["owner"] != mainID {
			owned++
		}
	}
	if owned != 0 {
		t.Fatalf("completed subagents should release their tabs, %d still owned", owned)
	}
	x := h.do(mainC, "GET", "/v1/cursors/"+ids[0], nil)
	if x["status"] != "done" {
		t.Fatal("parent cannot read child")
	}
	var e map[string]any
	if err := h.as(toks[1]).Do(context.Background(), "GET", "/v1/cursors/"+ids[2], nil, &e); err == nil {
		t.Fatal("sibling should not access sibling")
	}
	t.Log("PASS B: 5 subagents in one workspace, one desktop, one Chromium, scoped tokens, results collected")
}

func TestC_MulticursorDesktop(t *testing.T) {
	if _, err := exec.LookPath("xterm"); err != nil {
		t.Skip("xterm missing")
	}
	h := newH(t)
	ws := h.workspace("multi")
	aID, a := h.cursor(h.admin, map[string]any{"name": "A", "workspace": ws})
	_, b := h.cursor(h.admin, map[string]any{"name": "B", "workspace": ws})
	home := filepath.Join(h.home, "workspaces", ws, "home")
	files := []string{filepath.Join(home, "a.txt"), filepath.Join(home, "b.txt")}
	for _, f := range files {
		h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/apps", map[string]any{"command": "xterm", "args": []string{"-e", "sh", "-c", "cat > " + f}})
	}
	var wins []map[string]any
	for i := 0; i < 50; i++ {
		wins = h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/windows")
		if len(wins) >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(wins) < 2 {
		t.Fatal("xterms did not appear")
	}
	time.Sleep(300 * time.Millisecond)
	wins = h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/windows")
	center := func(w map[string]any) (float64, float64) {
		return w["x"].(float64) + w["width"].(float64)/2, w["y"].(float64) + w["height"].(float64)/2
	}
	byPid := map[float64]map[string]any{}
	for _, w := range wins {
		byPid[w["pid"].(float64)] = w
	}
	procs := h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/processes")
	_ = procs
	ax, ay := center(wins[0])
	bx, by := center(wins[1])
	h.do(a, "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "click", "x": ax, "y": ay})
	h.do(b, "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "click", "x": bx, "y": by})
	ta := "alpha typed by cursor A 111\n"
	tb := "bravo typed by cursor B 222\n"
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.do(a, "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "type", "text": ta, "delay_ms": 3})
	}()
	go func() {
		defer wg.Done()
		h.do(b, "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "type", "text": tb, "delay_ms": 3})
	}()
	wg.Wait()
	time.Sleep(500 * time.Millisecond)
	got := []string{}
	for _, f := range files {
		d, _ := os.ReadFile(f)
		got = append(got, strings.TrimSpace(string(d)))
	}
	okA := (got[0] == strings.TrimSpace(ta) && got[1] == strings.TrimSpace(tb)) || (got[1] == strings.TrimSpace(ta) && got[0] == strings.TrimSpace(tb))
	if !okA {
		t.Fatalf("concurrent typing mixed: %q", got)
	}
	v := h.wsView(ws)
	t.Logf("seats in use: %v", v["seats"])
	h.do(a, "POST", "/v1/workspaces/"+ws+"/acquire", map[string]any{"surface": fmt.Sprintf("win:%d", int(wins[0]["id"].(float64))), "reason": "editing"})
	var out map[string]any
	err := b.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "click", "x": ax, "y": ay}, &out)
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("expected conflict for B on A's window, got %v", err)
	}
	h.do(a, "POST", "/v1/workspaces/"+ws+"/release", map[string]any{"surface": fmt.Sprintf("win:%d", int(wins[0]["id"].(float64)))})
	h.do(b, "POST", "/v1/workspaces/"+ws+"/input", map[string]any{"action": "move", "x": ax, "y": ay})
	_ = aID
	t.Log("PASS C: two cursors with independent pointer+keyboard seats typed concurrently into different windows; exclusive surface lock enforced")
}

func TestD_ChallengeTakeover(t *testing.T) {
	h := newH(t)
	ws := h.workspace("captcha")
	cid, c := h.cursor(h.admin, map[string]any{"name": "shopper", "workspace": ws})
	tab := h.tab(c, ws, h.site.URL+"/login")
	h.act(c, ws, tab, map[string]any{"action": "navigate", "url": h.site.URL + "/challenge"})
	ev := h.waitEvent(ws, "browser.challenge.detected", 15*time.Second)
	t.Logf("detected: %v", ev["data"])
	cur := h.do(h.admin, "GET", "/v1/cursors/"+cid, nil)
	if cur["status"] != "needs_human" {
		t.Fatalf("cursor should need a human, got %v", cur["status"])
	}
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/control-take", nil)
	var out map[string]any
	err := c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tab, map[string]any{"action": "click", "x": 30, "y": 90}, &out)
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("agent input should be blocked during takeover: %v", err)
	}
	snap := h.act(h.admin, ws, tab, map[string]any{"action": "snapshot"})
	if snap["challenge"] == nil {
		t.Fatal("snapshot should flag challenge")
	}
	h.act(h.admin, ws, tab, map[string]any{"action": "eval", "expression": "document.getElementById('human').click()"})
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/control-return", nil)
	h.waitEvent(ws, "control.returned", 5*time.Second)
	cur = h.do(h.admin, "GET", "/v1/cursors/"+cid, nil)
	if cur["status"] != "running" {
		t.Fatalf("cursor should resume, got %v", cur["status"])
	}
	h.waitEval(c, ws, tab, "document.getElementById('st').textContent", "verified", 5*time.Second)
	tabs := h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/tabs")
	same := false
	for _, x := range tabs {
		if x["target"] == tab {
			same = true
		}
	}
	if !same {
		t.Fatal("tab changed during takeover")
	}
	t.Log("PASS D: challenge detected, agent marked needs_human, human takeover blocked agent input, same tab/session kept, agent resumed")
}

func cpuTicks(pids []int) uint64 {
	var total uint64
	for _, p := range pids {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p))
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		f := strings.Fields(s[i+2:])
		var u, st uint64
		fmt.Sscan(f[11], &u)
		fmt.Sscan(f[12], &st)
		total += u + st
	}
	return total
}

func TestE_FreezeSuspend(t *testing.T) {
	h := newH(t)
	ws := h.workspace("freeze")
	_, c := h.cursor(h.admin, map[string]any{"name": "f", "workspace": ws})
	tab := h.tab(c, ws, "data:text/html,<title>busy</title><script>setInterval(()=>{let x=0;for(let i=0;i<2e6;i++)x+=i;document.title='t'+x%7},50)</script>")
	time.Sleep(2 * time.Second)
	time.Sleep(10500 * time.Millisecond)
	v := h.wsView(ws)
	u := v["usage"].(map[string]any)
	t.Logf("active: pss=%.0fMB cpu=%.1f%% procs=%v", u["pss_mb"], u["cpu_percent"], u["procs"])
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/freeze", nil)
	pids := wsPids(t, h, ws)
	t1 := cpuTicks(pids)
	time.Sleep(3 * time.Second)
	t2 := cpuTicks(pids)
	t.Logf("frozen: %d processes, cpu ticks in 3s = %d", len(pids), t2-t1)
	if t2-t1 > 2 {
		t.Fatalf("frozen workspace still uses CPU (%d ticks)", t2-t1)
	}
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/thaw", nil)
	h.waitEval(c, ws, tab, "document.title.startsWith('t')", true, 10*time.Second)
	h.do(h.admin, "POST", "/v1/workspaces/"+ws+"/suspend", nil)
	v = h.wsView(ws)
	if v["state"] != "suspended" || v["display"] != nil {
		t.Fatalf("not suspended: %v", v)
	}
	if n := countProcs(t, "remote-debugging-pipe --user-data-dir="+h.home); n != 0 {
		t.Fatalf("browser still running after suspend: %d", n)
	}
	t.Log("PASS E: freeze stops all CPU and thaws with the page alive; suspend frees processes")
}

func wsPids(t *testing.T, h *H, ws string) []int {
	out, _ := exec.Command("pgrep", "-f", h.home).Output()
	var pids []int
	for _, l := range strings.Fields(string(out)) {
		var p int
		fmt.Sscan(l, &p)
		pids = append(pids, p)
	}
	xo, _ := exec.Command("pgrep", "-f", "Xvfb .*"+h.home).Output()
	for _, l := range strings.Fields(string(xo)) {
		var p int
		fmt.Sscan(l, &p)
		pids = append(pids, p)
	}
	return pids
}

func TestFG_NoViewerAndReconnect(t *testing.T) {
	h := newH(t)
	ws := h.workspace("stream")
	_, c := h.cursor(h.admin, map[string]any{"name": "s", "workspace": ws})
	tab := h.tab(c, ws, "data:text/html,<title>anim</title><div id=d></div><script>setInterval(()=>d.textContent=Date.now(),30)</script>")
	time.Sleep(3 * time.Second)
	st := h.wsView(ws)["stream"].(map[string]any)
	if st["running"] == true || st["frames"].(float64) != 0 {
		t.Fatalf("encoding without viewers: %v", st)
	}
	t.Logf("no viewer, animated page for 3s: frames=%v starts=%v", st["frames"], st["starts"])
	vt := h.do(h.admin, "POST", "/v1/viewer-tokens", map[string]any{"workspace": ws, "control": true})["token"].(string)
	url := strings.Replace(h.srv.URL, "http", "ws", 1) + "/v1/workspaces/" + ws + "/stream/ws?token=" + vt
	count := func(d time.Duration) (int, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), d+3*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		key := false
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			rctx, rc := context.WithDeadline(ctx, end)
			typ, b, err := conn.Read(rctx)
			rc()
			if err != nil {
				break
			}
			if typ == websocket.MessageBinary && len(b) > 13 {
				if n == 0 && b[0]&1 == 1 {
					key = true
				}
				_ = binary.BigEndian.Uint16(b[9:])
				n++
			}
		}
		conn.CloseNow()
		return n, key
	}
	n1, k1 := count(2 * time.Second)
	t.Logf("viewer 1: %d frames in 2s, first is keyframe=%v", n1, k1)
	if n1 < 10 || !k1 {
		t.Fatalf("expected streaming starting with keyframe")
	}
	time.Sleep(1500 * time.Millisecond)
	st = h.wsView(ws)["stream"].(map[string]any)
	if st["running"] == true {
		t.Fatalf("encoder should stop after abrupt disconnect: %v", st)
	}
	h.waitEval(c, ws, tab, "document.title", "anim", 3*time.Second)
	n2, k2 := count(2 * time.Second)
	t.Logf("reconnect: %d frames, keyframe=%v", n2, k2)
	if n2 < 10 || !k2 {
		t.Fatal("reconnect failed")
	}
	fds := func() int { e, _ := os.ReadDir("/proc/self/fd"); return len(e) }
	before := fds()
	for i := 0; i < 8; i++ {
		count(300 * time.Millisecond)
	}
	time.Sleep(time.Second)
	if after := fds(); after > before+2 {
		t.Fatalf("viewer connections leak file descriptors: %d before, %d after 8 viewers", before, after)
	}
	t.Log("PASS F+G: zero encoding without viewers; abrupt disconnect stops encoder, workspace keeps running, reconnect resumes with keyframe")
}

func TestUploadDownloadDialogDnD(t *testing.T) {
	h := newH(t)
	ws := h.workspace("compat")
	_, c := h.cursor(h.admin, map[string]any{"name": "compat", "workspace": ws})
	tab := h.tab(c, ws, h.site.URL+"/upload")
	f := filepath.Join(h.home, "workspaces", ws, "home", "Downloads", "agora-upload.txt")
	if err := os.WriteFile(f, []byte("hello upload"), 0644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(f, 0644)
	h.act(c, ws, tab, map[string]any{"action": "snapshot"})
	h.act(c, ws, tab, map[string]any{"action": "upload", "ref": "e1", "files": []string{f}})
	h.act(c, ws, tab, map[string]any{"action": "click", "ref": "e2"})
	h.waitEval(c, ws, tab, "document.title", "Uploaded:"+filepath.Base(f), 10*time.Second)
	h.act(c, ws, tab, map[string]any{"action": "navigate", "url": h.site.URL + "/upload"})
	h.act(c, ws, tab, map[string]any{"action": "snapshot"})
	h.act(c, ws, tab, map[string]any{"action": "click", "ref": "e3"})
	ev := h.waitEvent(ws, "browser.download.completed", 10*time.Second)
	t.Logf("download: %v", ev["data"])
	h.act(c, ws, tab, map[string]any{"action": "navigate", "url": h.site.URL + "/dialog"})
	go func() {
		time.Sleep(300 * time.Millisecond)
	}()
	var out map[string]any
	go c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+tab, map[string]any{"action": "click", "x": 30, "y": 30}, &out)
	h.waitEvent(ws, "browser.dialog.opened", 5*time.Second)
	h.act(c, ws, tab, map[string]any{"action": "dialog", "accept": true})
	h.waitEval(c, ws, tab, "document.title", "confirm:true", 5*time.Second)
	h.act(c, ws, tab, map[string]any{"action": "navigate", "url": h.site.URL + "/dnd"})
	h.act(c, ws, tab, map[string]any{"action": "drag", "x": 60, "y": 60, "x2": 100, "y2": 200})
	h.waitEval(c, ws, tab, "document.title", "dropped", 5*time.Second)
	t.Log("PASS compat: upload, download, JS dialog, drag")
}

func TestDeletedCursorClosesItsTabsAndApps(t *testing.T) {
	if _, err := exec.LookPath("xterm"); err != nil {
		t.Skip("xterm missing")
	}
	h := newH(t)
	ws := h.workspace("cleanup")
	id, c := h.cursor(h.admin, map[string]any{"name": "paseo", "workspace": ws, "labels": map[string]string{"keep_tabs": "true"}})
	target := h.tab(c, ws, "about:blank")
	app := h.do(c, "POST", "/v1/workspaces/"+ws+"/apps", map[string]any{"command": "xterm"})
	pid := int(app["pid"].(float64))
	h.do(h.admin, "DELETE", "/v1/cursors/"+id, nil)
	deadline := time.Now().Add(10 * time.Second)
	for {
		tabOpen := false
		for _, tb := range h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/tabs") {
			if tb["target"] == target {
				tabOpen = true
			}
		}
		appAlive := false
		for _, p := range h.doList(h.admin, "GET", "/v1/workspaces/"+ws+"/processes") {
			if int(p["pid"].(float64)) == pid {
				appAlive = true
			}
		}
		if !tabOpen && !appAlive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("deleted cursor left tab open=%v app alive=%v", tabOpen, appAlive)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
