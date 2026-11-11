package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/api"
	"github.com/olimpiacloud/agora/internal/client"
	"github.com/olimpiacloud/agora/internal/core"
	"github.com/olimpiacloud/agora/internal/stream"
)

type H struct {
	t     *testing.T
	m     *core.Manager
	srv   *httptest.Server
	site  *httptest.Server
	admin *client.Client
	home  string
}

func need(t *testing.T) {
	for _, b := range []string{"Xvfb", "google-chrome"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip(b + " missing")
		}
	}
}

func newH(t *testing.T) *H {
	return newHWith(t, nil)
}

func newHWith(t *testing.T, tune func(*core.Config)) *H {
	need(t)
	home, err := os.MkdirTemp("/tmp", "agora-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(home, 0711)
	appsUser := ""
	if os.Getuid() == 0 {
		appsUser = "agora"
	}
	cfg := core.Config{Home: home, AppsUser: appsUser, IdleAfter: time.Hour, RTC: stream.RTCOptions{IncludeLoop: true}}
	if tune != nil {
		tune(&cfg)
	}
	m, err := core.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewAuth(home, m)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(m, auth, nil))
	tok, _ := os.ReadFile(filepath.Join(home, "admin.token"))
	h := &H{t: t, m: m, srv: srv, site: httptest.NewServer(testSite()), home: home}
	h.admin = &client.Client{URL: srv.URL, Token: strings.TrimSpace(string(tok)), HTTP: &http.Client{Timeout: 2 * time.Minute}}
	t.Cleanup(func() {
		m.Shutdown()
		srv.Close()
		h.site.Close()
		if os.Getenv("AGORA_KEEP") == "" {
			os.RemoveAll(home)
		}
	})
	return h
}

func (h *H) as(tok string) *client.Client {
	return &client.Client{URL: h.srv.URL, Token: tok, HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

func (h *H) do(c *client.Client, method, path string, body any) map[string]any {
	h.t.Helper()
	var out map[string]any
	if err := c.Do(context.Background(), method, path, body, &out); err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return out
}

func (h *H) doList(c *client.Client, method, path string) []map[string]any {
	h.t.Helper()
	var out []map[string]any
	if err := c.Do(context.Background(), method, path, nil, &out); err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return out
}

func (h *H) workspace(name string) string {
	w := h.do(h.admin, "POST", "/v1/workspaces", map[string]any{"name": name, "start": true})
	return w["id"].(string)
}

func (h *H) cursor(c *client.Client, body map[string]any) (string, *client.Client) {
	r := h.do(c, "POST", "/v1/cursors", body)
	cur := r["cursor"].(map[string]any)
	return cur["id"].(string), h.as(r["token"].(string))
}

func (h *H) tab(c *client.Client, ws, url string) string {
	r := h.do(c, "POST", "/v1/workspaces/"+ws+"/tabs", map[string]any{"url": url})
	return r["target"].(string)
}

func (h *H) act(c *client.Client, ws, target string, a map[string]any) map[string]any {
	return h.do(c, "POST", "/v1/workspaces/"+ws+"/tabs/"+target, a)
}

func (h *H) eval(c *client.Client, ws, target, expr string) any {
	return h.act(c, ws, target, map[string]any{"action": "eval", "expression": expr})["value"]
}

func (h *H) waitEval(c *client.Client, ws, target, expr string, want any, d time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(d)
	var last any
	for time.Now().Before(deadline) {
		var out map[string]any
		if err := c.Do(context.Background(), "POST", "/v1/workspaces/"+ws+"/tabs/"+target, map[string]any{"action": "eval", "expression": expr}, &out); err == nil {
			last = out["value"]
			if fmt.Sprint(last) == fmt.Sprint(want) {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	h.t.Fatalf("eval %q = %v, want %v", expr, last, want)
}

func (h *H) waitEvent(ws, typ string, d time.Duration) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(d)
	var since float64
	for time.Now().Before(deadline) {
		r := h.do(h.admin, "GET", fmt.Sprintf("/v1/events?workspace=%s&types=%s&since=%d&wait_ms=1000", ws, typ, int64(since)), nil)
		evs, _ := r["events"].([]any)
		if len(evs) > 0 {
			return evs[len(evs)-1].(map[string]any)
		}
	}
	h.t.Fatalf("event %s not seen", typ)
	return nil
}

func (h *H) wsView(ws string) map[string]any {
	return h.do(h.admin, "GET", "/v1/workspaces/"+ws, nil)
}

func countProcs(t *testing.T, match string) int {
	out, _ := exec.Command("pgrep", "-c", "-f", match).Output()
	var n int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

var siteMu sync.Mutex

func testSite() http.Handler {
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, title, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>%s</title><body style='font:16px sans-serif;padding:20px'>%s</body>", title, body)
	}
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			r.ParseForm()
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "user-" + r.Form.Get("user"), Path: "/", MaxAge: 86400 * 30, HttpOnly: true, SameSite: http.SameSiteLaxMode})
			http.Redirect(w, r, "/me", http.StatusFound)
			return
		}
		page(w, "Login", `<form method=post><label>User <input name=user aria-label=user></label> <label>Password <input type=password name=pass aria-label=password></label> <button>Sign in</button></form>`)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("session")
		who := "anonymous"
		if err == nil {
			who = c.Value
		}
		page(w, "Me:"+who, fmt.Sprintf(`<h1 id=who>%s</h1><script>
localStorage.setItem('ls', localStorage.getItem('ls') || 'v-'+Date.now());
const rq = indexedDB.open('agora', 1); rq.onupgradeneeded = () => rq.result.createObjectStore('kv');
rq.onsuccess = () => { const db = rq.result; const tx = db.transaction('kv', 'readwrite'); const st = tx.objectStore('kv'); const g = st.get('k'); g.onsuccess = () => { if (!g.result) st.put('idb-'+Date.now(), 'k'); }; };
</script>`, who))
	})
	mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("n")
		page(w, "Work "+n, fmt.Sprintf(`<p id=secret>secret-%s</p><input aria-label=answer id=a><button id=b onclick="document.title='answered:'+a.value">Submit</button>`, n))
	})
	mux.HandleFunc("/challenge", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Verify", `<h2>Please verify</h2><div class="g-recaptcha" data-sitekey="test" style="width:300px;height:80px;border:1px solid #999"><label><input type=checkbox id=human onchange="document.getElementById('st').textContent=this.checked?'verified':'pending'"> I'm not a robot</label></div><p id=st>pending</p>`)
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			r.ParseMultipartForm(1 << 20)
			f, hd, err := r.FormFile("f")
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			defer f.Close()
			page(w, "Uploaded:"+hd.Filename, "ok")
			return
		}
		page(w, "Upload", `<form method=post enctype=multipart/form-data><input type=file name=f aria-label=file><button>Send</button></form><a href="/download" download>dl</a>`)
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", "attachment; filename=report.txt")
		w.Write([]byte("agora download ok"))
	})
	mux.HandleFunc("/dnd", func(w http.ResponseWriter, r *http.Request) {
		page(w, "DnD", `<div id=src draggable=true style="width:80px;height:80px;background:#4af">drag</div><div id=dst style="margin-top:60px;width:200px;height:120px;border:2px dashed #999">drop</div>
<script>dst.addEventListener('dragover',e=>e.preventDefault());dst.addEventListener('drop',e=>{e.preventDefault();document.title='dropped'});let x0,y0;src.addEventListener('mousedown',e=>{x0=e.clientX;y0=e.clientY});document.addEventListener('mouseup',e=>{const r=dst.getBoundingClientRect();if(x0!==undefined&&e.clientX>r.left&&e.clientX<r.right&&e.clientY>r.top&&e.clientY<r.bottom)document.title='dropped';x0=undefined});</script>`)
	})
	mux.HandleFunc("/dialog", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Dialog", `<button id=b onclick="document.title='confirm:'+confirm('sure?')">ask</button>`)
	})
	return mux
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
