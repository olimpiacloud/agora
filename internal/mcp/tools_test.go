package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/olimpiacloud/agora/internal/client"
)

func TestSlimCursorList(t *testing.T) {
	c := map[string]any{
		"id": "c1", "name": "n", "status": "done", "goal": strings.Repeat("g", 400),
		"result":     map[string]any{"text": strings.Repeat("é", 500)},
		"token_hash": "h", "color": "#fff", "x": 1.0, "y": 2.0,
		"launch":   map[string]any{"adapter": "claude", "prompt": "p", "env": map[string]any{"K": "v"}},
		"usage":    map[string]any{"cost_usd": 0.1, "turns": 3.0, "input_tokens": 10.0},
		"progress": []any{map[string]any{"message": "a"}, map[string]any{"message": "b"}},
	}
	b, _ := json.Marshal(c)
	var full map[string]any
	json.Unmarshal(b, &full)

	out := slimCursor(c, listResultMax).(map[string]any)
	for _, k := range []string{"token_hash", "color", "x", "launch", "progress"} {
		if _, ok := out[k]; ok {
			t.Fatalf("list view kept %s", k)
		}
	}
	if out["last_progress"] != "b" || out["result_truncated"] != true {
		t.Fatalf("list view: %v", out)
	}
	if r := out["result"].(string); len(r) > listResultMax+len("…") || !strings.HasSuffix(r, "…") {
		t.Fatalf("result preview: %d bytes", len(r))
	}
	if u := out["usage"].(map[string]any); len(u) != 2 {
		t.Fatalf("usage: %v", u)
	}

	got := slimCursor(full, -1).(map[string]any)
	if _, ok := got["token_hash"]; ok {
		t.Fatal("get view kept token_hash")
	}
	l := got["launch"].(map[string]any)
	if _, ok := l["env"]; ok || l["adapter"] != "claude" {
		t.Fatalf("get view launch: %v", l)
	}
	if _, ok := got["result"].(map[string]any); !ok {
		t.Fatal("get view must keep the full result")
	}
}

func TestShotMapping(t *testing.T) {
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1024, 640)), nil)
	if m := shotScale(buf.Bytes(), 1024, 640, 0, 0); m != nil {
		t.Fatal("identity should not map")
	}
	s := &Server{}
	s.setShot("ws:w", shotScale(buf.Bytes(), 1600, 1000, 0, 0))
	body := map[string]any{"x": 512.0, "y": 320.0}
	s.mapPoints("ws:w", Args{"x": 512.0, "y": 320.0}, body)
	if body["x"] != 800.0 || body["y"] != 500.0 {
		t.Fatalf("mapped to %v", body)
	}
	s.setShot("ws:r", shotScale(buf.Bytes(), 512, 320, 100, 50))
	body = map[string]any{"x": 0.0, "y": 0.0, "x2": 1024.0, "y2": 640.0}
	s.mapPoints("ws:r", Args{"x": 0.0, "y": 0.0, "x2": 1024.0, "y2": 640.0}, body)
	if body["x"] != 100.0 || body["y"] != 50.0 || body["x2"] != 612.0 || body["y2"] != 370.0 {
		t.Fatalf("region mapped to %v", body)
	}
	body = map[string]any{"dy": 3.0}
	s.mapPoints("ws:w", Args{"dy": 3.0}, body)
	if _, ok := body["x"]; ok {
		t.Fatal("absent coordinates must stay absent")
	}
	s.setShot("ws:w", nil)
	body = map[string]any{"x": 10.0, "y": 10.0}
	s.mapPoints("ws:w", Args{"x": 10.0, "y": 10.0}, body)
	if body["x"] != 10.0 {
		t.Fatal("cleared mapping still applied")
	}
}

func fakeServer(t *testing.T, reply func(path string, body map[string]any) any) (*Server, func() map[string]any) {
	var mu sync.Mutex
	var bodies []map[string]any
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		json.NewEncoder(w).Encode(reply(r.URL.Path, b))
	}))
	t.Cleanup(hs.Close)
	s := New(&client.Client{URL: hs.URL, HTTP: hs.Client()})
	return s, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return bodies[len(bodies)-1]
	}
}

func runTool(t *testing.T, s *Server, name string, a Args) []content {
	t.Helper()
	out, err := s.byName[name].Run(context.Background(), s, a)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestShotMappingClearedByCSSCoordinates(t *testing.T) {
	var shot bytes.Buffer
	jpeg.Encode(&shot, image.NewRGBA(image.Rect(0, 0, 1024, 640)), nil)
	s, last := fakeServer(t, func(path string, b map[string]any) any {
		switch b["action"] {
		case "screenshot":
			return map[string]any{"image": base64.StdEncoding.EncodeToString(shot.Bytes()), "mime": "image/jpeg", "viewport": []float64{1600, 1000}}
		case "snapshot":
			return map[string]any{"snapshot": "[e1] button \"A\" @10,10 5x5"}
		}
		return map[string]any{"ok": true}
	})
	tab := Args{"workspace": "w", "target": "T"}
	with := func(kv ...any) Args {
		a := Args{}
		for k, v := range tab {
			a[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			a[kv[i].(string)] = kv[i+1]
		}
		return a
	}
	runTool(t, s, "browser_screenshot", with())
	runTool(t, s, "browser_click", with("x", 512.0, "y", 320.0, "observe", false))
	if b := last(); b["x"] != 800.0 || b["y"] != 500.0 {
		t.Fatalf("screenshot coordinates not mapped: %v", b)
	}
	runTool(t, s, "browser_snapshot", with())
	runTool(t, s, "browser_click", with("x", 512.0, "y", 320.0, "observe", false))
	if b := last(); b["x"] != 800.0 {
		t.Fatalf("snapshot without boxes must keep the mapping: %v", b)
	}
	runTool(t, s, "browser_snapshot", with("boxes", true))
	runTool(t, s, "browser_click", with("x", 512.0, "y", 320.0, "observe", false))
	if b := last(); b["x"] != 512.0 || b["y"] != 320.0 {
		t.Fatalf("boxes coordinates were scaled: %v", b)
	}

	runTool(t, s, "browser_screenshot", with())
	runTool(t, s, "workspace_op", Args{"workspace": "w", "op": "resize", "width": 800.0, "height": 600.0})
	runTool(t, s, "browser_hover", with("x", 512.0, "y", 320.0, "observe", false))
	if b := last(); b["x"] != 512.0 {
		t.Fatalf("resize kept the tab mapping: %v", b)
	}

	s.setShot("tab:w2/T", &shotMap{sx: 2, sy: 2})
	runTool(t, s, "workspace_op", Args{"workspace": "w", "op": "resize", "width": 800.0, "height": 600.0})
	if _, ok := s.shots["tab:w2/T"]; !ok {
		t.Fatal("resize of w cleared another workspace's mapping")
	}
}

func TestDesktopMappingClearedByWindows(t *testing.T) {
	s, last := fakeServer(t, func(path string, b map[string]any) any { return map[string]any{"ok": true} })
	s.setShot("ws:w", &shotMap{sx: 2, sy: 2})
	s.setShot("ws:w2", &shotMap{sx: 2, sy: 2})
	runTool(t, s, "desktop_click", Args{"workspace": "w", "x": 10.0, "y": 10.0})
	if b := last(); b["x"] != 20.0 {
		t.Fatalf("screenshot mapping not applied: %v", b)
	}
	runTool(t, s, "desktop_windows", Args{"workspace": "w"})
	runTool(t, s, "desktop_click", Args{"workspace": "w", "x": 10.0, "y": 10.0})
	if b := last(); b["x"] != 10.0 {
		t.Fatalf("window geometry was scaled: %v", b)
	}
	s.setShot("ws:w", &shotMap{sx: 2, sy: 2})
	runTool(t, s, "workspace_op", Args{"workspace": "w", "op": "resize", "width": 800.0, "height": 600.0})
	runTool(t, s, "desktop_move", Args{"workspace": "w", "x": 10.0, "y": 10.0})
	if b := last(); b["x"] != 10.0 {
		t.Fatalf("resize kept the desktop mapping: %v", b)
	}
	if _, ok := s.shots["ws:w2"]; !ok {
		t.Fatal("other workspace mapping cleared")
	}
}

func TestEvalResultCapped(t *testing.T) {
	big := strings.Repeat("é", 40<<10)
	s, _ := fakeServer(t, func(path string, b map[string]any) any { return map[string]any{"ok": true, "value": big} })
	out := runTool(t, s, "browser_eval", Args{"workspace": "w", "target": "T", "expression": "x"})
	txt := out[0].Text
	if len(txt) > evalMax+64 || !strings.Contains(txt, "[truncated ") || !utf8.ValidString(txt) {
		t.Fatalf("eval result %d bytes, valid=%v", len(txt), utf8.ValidString(txt))
	}
	s, _ = fakeServer(t, func(path string, b map[string]any) any { return map[string]any{"ok": true, "value": "small"} })
	if out := runTool(t, s, "browser_eval", Args{"workspace": "w", "target": "T", "expression": "x"}); strings.Contains(out[0].Text, "truncated") {
		t.Fatalf("small result truncated: %q", out[0].Text)
	}
}
