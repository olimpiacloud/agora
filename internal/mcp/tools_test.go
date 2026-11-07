package mcp

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"strings"
	"testing"
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
