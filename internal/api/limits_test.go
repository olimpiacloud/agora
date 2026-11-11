package api

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/olimpiacloud/agora/internal/core"
)

func limitEnv(t *testing.T) (*env, string, string) {
	e := newEnv(t)
	ws := e.workspace()
	id, tok := e.cursor(e.admin, map[string]any{"workspace": ws, "permissions": []string{"message"}})
	return e, id, tok
}

func bigText() string {
	return strings.Repeat("x", 100<<10)
}

func TestProgressBounded(t *testing.T) {
	e, id, tok := limitEnv(t)
	if code, out := e.req(tok, "POST", "/v1/cursors/self/report", map[string]any{"message": bigText()}); code != 200 {
		t.Fatalf("report: %d %v", code, out)
	}
	c, err := e.m.GetCursor(id)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(c.Progress[len(c.Progress)-1].Message); n > 2000 {
		t.Fatalf("progress message kept %d runes", n)
	}
	evs := e.m.Bus.Recent(core.Filter{Types: []string{"cursor.progress"}}, 5)
	if len(evs) == 0 {
		t.Fatal("no cursor.progress event")
	}
	for _, ev := range evs {
		if n := utf8.RuneCountInString(ev.Data["message"].(string)); n > 2000 {
			t.Fatalf("progress event carries %d runes", n)
		}
	}
}

func TestStatusDetailBounded(t *testing.T) {
	e, id, tok := limitEnv(t)
	if code, out := e.req(tok, "POST", "/v1/cursors/self/status", map[string]any{"status": "running", "detail": bigText()}); code != 200 {
		t.Fatalf("status: %d %v", code, out)
	}
	c, _ := e.m.GetCursor(id)
	if n := utf8.RuneCountInString(c.Detail); n > 2000 {
		t.Fatalf("detail kept %d runes", n)
	}
}

func TestResultLimit(t *testing.T) {
	e, id, tok := limitEnv(t)
	if code, out := e.req(tok, "POST", "/v1/cursors/self/complete", map[string]any{"result": strings.Repeat("y", 300<<10)}); code != 400 {
		t.Fatalf("complete with a large result: %d %v", code, out)
	}
	c, _ := e.m.GetCursor(id)
	if c.Status.Terminal() {
		t.Fatalf("cursor ended with status %s", c.Status)
	}
}

func TestMessageBodyLimit(t *testing.T) {
	e, _, tok := limitEnv(t)
	if code, out := e.req(tok, "POST", "/v1/cursors/self/message", map[string]any{"body": bigText()}); code != 400 {
		t.Fatalf("large message: %d %v", code, out)
	}
	if code, out := e.req(tok, "POST", "/v1/cursors/self/message", map[string]any{"body": "hi"}); code != 200 {
		t.Fatalf("small message: %d %v", code, out)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	e, _, tok := limitEnv(t)
	if code, out := e.req(tok, "POST", "/v1/cursors/self/report", map[string]any{"message": strings.Repeat("x", 600<<10)}); code != 400 {
		t.Fatalf("oversized report: %d %v", code, out)
	}
}

func TestCreateCursorLimits(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	labels := map[string]string{}
	for i := 0; i < 40; i++ {
		labels[fmt.Sprintf("k%d", i)] = "v"
	}
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"long name", map[string]any{"workspace": ws, "name": strings.Repeat("n", 300)}, 400},
		{"many labels", map[string]any{"workspace": ws, "labels": labels}, 400},
		{"long goal", map[string]any{"workspace": ws, "goal": strings.Repeat("g", 70<<10)}, 400},
		{"normal", map[string]any{"workspace": ws, "name": "ok", "goal": "do it"}, 201},
	}
	for _, c := range cases {
		if code, out := e.req(e.admin, "POST", "/v1/cursors", c.body); code != c.want {
			t.Errorf("%s: %d %v, want %d", c.name, code, out, c.want)
		}
	}
}

func TestCreateWorkspaceLimit(t *testing.T) {
	e := newEnv(t)
	if code, out := e.req(e.admin, "POST", "/v1/workspaces", map[string]any{"name": strings.Repeat("w", 300)}); code != 400 {
		t.Fatalf("long workspace name: %d %v", code, out)
	}
}

func TestCreatedEventGoalBounded(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	if code, out := e.req(e.admin, "POST", "/v1/cursors", map[string]any{"workspace": ws, "goal": strings.Repeat("g", 10<<10)}); code != 201 {
		t.Fatalf("create cursor: %d %v", code, out)
	}
	evs := e.m.Bus.Recent(core.Filter{Types: []string{"cursor.created"}}, 1)
	if len(evs) != 1 {
		t.Fatal("no cursor.created event")
	}
	if n := utf8.RuneCountInString(evs[0].Data["goal"].(string)); n > 500 {
		t.Fatalf("cursor.created carries a goal of %d runes", n)
	}
}
