package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

func str(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
func num(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
func boo(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
func strs(d string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": d}
}
func anyv(d string) map[string]any { return map[string]any{"description": d} }

var wsProp = str("Workspace id (defaults to your own workspace)")

func with(p map[string]any) map[string]any {
	p["workspace"] = wsProp
	return p
}

func tabProp(p map[string]any) map[string]any {
	p["workspace"] = wsProp
	p["target"] = str("Tab target id from browser_open/browser_tabs")
	return p
}

var pointing = map[string]bool{"click": true, "hover": true, "drag": true, "scroll": true}

var observing = map[string]bool{"scroll": true, "navigate": true, "click": true, "fill": true, "type": true, "press": true, "select": true, "back": true, "forward": true, "reload": true, "hover": true, "drag": true, "upload": true, "dialog": true}

func (s *Server) tab(ctx context.Context, a Args, body map[string]any) ([]content, error) {
	if act, _ := body["action"].(string); observing[act] {
		if v, ok := a["observe"].(bool); !ok || v {
			body["observe"] = true
		}
	}
	ws, err := s.wsArg(a)
	if err != nil {
		return nil, err
	}
	t := a.S("target")
	if t == "" {
		return nil, fmt.Errorf("target is required")
	}
	act, _ := body["action"].(string)
	key := "tab:" + ws + "/" + t
	if pointing[act] && a.S("ref") == "" {
		s.mapPoints(key, a, body)
	}
	var out map[string]any
	if err := s.c.Do(ctx, "POST", "/v1/workspaces/"+ws+"/tabs/"+t, body, &out); err != nil {
		return nil, err
	}
	if act == "emulate" || (act == "snapshot" && body["boxes"] == true) {
		s.setShot(key, nil)
	}
	if img, ok := out["image"].(string); ok {
		res := []content{{Type: "image", Data: img, MimeType: fmt.Sprint(out["mime"])}}
		note := ""
		if full, _ := out["full_page"].(bool); full {
			s.setShot(key, nil)
			note = fullNote
		} else if vp, ok := out["viewport"].([]any); ok && len(vp) == 2 {
			raw, _ := base64.StdEncoding.DecodeString(img)
			vw, _ := vp[0].(float64)
			vh, _ := vp[1].(float64)
			m := shotScale(raw, vw, vh, 0, 0)
			s.setShot(key, m)
			if m != nil {
				note = tabNote
			}
		}
		id, _ := out["shot"].(string)
		if txt := shotText(id, note); txt != "" {
			res = append(res, content{Type: "text", Text: txt})
		}
		return res, nil
	}
	if obs, ok := out["observation"].(string); ok {
		extra := ""
		if ch, ok := out["challenge"]; ok && ch != nil {
			b, _ := json.Marshal(ch)
			extra = "\nCHALLENGE DETECTED: " + string(b)
		}
		return text("done. now visible:\n" + obs + extra), nil
	}
	if snap, ok := out["snapshot"].(string); ok {
		extra := ""
		if ch, ok := out["challenge"]; ok && ch != nil {
			b, _ := json.Marshal(ch)
			extra = "\nCHALLENGE DETECTED: " + string(b) + " (interact with it visually if allowed, or call human_request)"
		}
		return text(snap + extra), nil
	}
	return textJSON(out), nil
}

func (s *Server) input(ctx context.Context, a Args, body map[string]any) ([]content, error) {
	ws, err := s.wsArg(a)
	if err != nil {
		return nil, err
	}
	s.mapPoints("ws:"+ws, a, body)
	return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/input", body)
}

func pick(a Args, keys ...string) map[string]any {
	m := map[string]any{}
	for _, k := range keys {
		if v, ok := a[k]; ok {
			m[k] = v
		}
	}
	return m
}

const (
	deskCoords = "Coordinates are positions in your last desktop_screenshot of this workspace; after desktop_windows, a resize or without a screenshot they are desktop pixels."
	tabCoords  = "Coordinates are positions in your last browser_screenshot of this tab; after browser_snapshot boxes=true, browser_emulate, a resize or without a screenshot they are CSS viewport pixels."
	evalMax    = 64 << 10
)

func capText(cs []content, n int) []content {
	for i := range cs {
		if cs[i].Type != "text" || len(cs[i].Text) <= n {
			continue
		}
		t := cs[i].Text
		cut := n
		for cut > 0 && !utf8.RuneStart(t[cut]) {
			cut--
		}
		cs[i].Text = t[:cut] + fmt.Sprintf("\n…[truncated %d bytes]", len(t)-cut)
	}
	return cs
}

func tools() []Tool {
	return []Tool{
		{Name: "workspace_list", Desc: "List workspaces with state, resources, viewers and cursors.", Props: map[string]any{},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "GET", "/v1/workspaces", nil)
			}},
		{Name: "workspace_create", Desc: "Create a workspace (a persistent virtual computer). It costs nothing until used.", Props: map[string]any{"name": str("Name"), "width": num("Screen width"), "height": num("Screen height"), "profile": str("Browser profile name to reuse (shared login state). Default: dedicated"), "start": boo("Start the desktop now"), "layout": str("tile | max | float")},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				cfg := map[string]any{"width": a.N("width"), "height": a.N("height"), "profile": a.S("profile"), "layout": a.S("layout")}
				return s.call(ctx, "POST", "/v1/workspaces", map[string]any{"name": a.S("name"), "start": a.B("start"), "config": cfg})
			}},
		{Name: "workspace_info", Desc: "Workspace details: state, display, browser, usage, locks.", Props: with(map[string]any{}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "GET", "/v1/workspaces/"+ws, nil)
			}},
		{Name: "workspace_op", Desc: "Lifecycle: start | suspend | resume | freeze | thaw | checkpoint | fork | resize | layout | browser-restart. resize drops the coordinate mapping of earlier screenshots.", Props: with(map[string]any{"op": str("Operation"), "name": str("fork name"), "width": num("resize width"), "height": num("resize height"), "layout": str("tile|max|float"), "snapshot": boo("checkpoint: also snapshot browser profile")}), Required: []string{"op"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				out, err := s.call(ctx, "POST", "/v1/workspaces/"+ws+"/"+a.S("op"), pick(a, "name", "width", "height", "layout", "snapshot"))
				if err == nil && a.S("op") == "resize" {
					s.setShot("ws:"+ws, nil)
					s.clearShots("tab:" + ws + "/")
				}
				return out, err
			}},
		{Name: "desktop_screenshot", Desc: "Capture the desktop (or one window / region) as an image, 1024 px wide JPEG by default; desktop_click/move/drag/scroll then take positions in that image. Region x/y/w/h are desktop pixels.", Props: with(map[string]any{"window": num("X window id"), "x": num("region x"), "y": num("region y"), "w": num("region width"), "h": num("region height"), "max_width": num("downscale to this width (default 1024)"), "format": str("jpeg|png"), "quality": num("JPEG quality (default 60)")}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				f := func(k string, d int) string {
					if _, ok := a[k]; !ok {
						return fmt.Sprint(d)
					}
					return fmt.Sprint(int(a.N(k)))
				}
				return s.image(ctx, "/v1/workspaces/"+ws+"/screenshot"+q("window", f("window", 0), "x", f("x", 0), "y", f("y", 0), "w", f("w", 0), "h", f("h", 0), "max_width", f("max_width", 1024), "quality", f("quality", 60), "format", a.S("format")), "ws:"+ws)
			}},
		{Name: "desktop_windows", Desc: "List top-level windows (id, title, class, geometry). Geometry is in desktop pixels: after this call desktop_click/move/drag/scroll take desktop pixels until the next desktop_screenshot.", Props: with(map[string]any{}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				s.setShot("ws:"+ws, nil)
				return s.call(ctx, "GET", "/v1/workspaces/"+ws+"/windows", nil)
			}},
		{Name: "desktop_click", Desc: "Click with your own pointer at x,y. Focuses and raises the window under it. " + deskCoords, Props: with(map[string]any{"x": num("x"), "y": num("y"), "button": num("1 left, 2 middle, 3 right"), "count": num("clicks (2 = double)")}), Required: []string{"x", "y"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "click", "x": a.N("x"), "y": a.N("y"), "button": a.N("button"), "count": a.N("count")})
			}},
		{Name: "desktop_move", Desc: "Move your pointer (hover). " + deskCoords, Props: with(map[string]any{"x": num("x"), "y": num("y")}), Required: []string{"x", "y"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "move", "x": a.N("x"), "y": a.N("y")})
			}},
		{Name: "desktop_drag", Desc: "Drag with the left button from (x,y) to (x2,y2). " + deskCoords, Props: with(map[string]any{"x": num("from x"), "y": num("from y"), "x2": num("to x"), "y2": num("to y")}), Required: []string{"x", "y", "x2", "y2"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "drag", "x": a.N("x"), "y": a.N("y"), "x2": a.N("x2"), "y2": a.N("y2")})
			}},
		{Name: "desktop_scroll", Desc: "Scroll at a position. dy>0 scrolls down (notches). " + deskCoords, Props: with(map[string]any{"x": num("x"), "y": num("y"), "dx": num("horizontal notches"), "dy": num("vertical notches")}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "scroll", "x": a.N("x"), "y": a.N("y"), "dx": a.N("dx"), "dy": a.N("dy")})
			}},
		{Name: "desktop_type", Desc: "Type text into the window focused by your keyboard (your own focus, independent of other cursors).", Props: with(map[string]any{"text": str("Text"), "window": num("Window id to focus first")}), Required: []string{"text"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "type", "text": a.S("text"), "window": a.N("window")})
			}},
		{Name: "desktop_key", Desc: "Press a key or combo, e.g. Enter, ctrl+l, ctrl+shift+t, alt+F4.", Props: with(map[string]any{"keys": str("Combo"), "window": num("Window id to focus first")}), Required: []string{"keys"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "key", "keys": a.S("keys"), "window": a.N("window")})
			}},
		{Name: "desktop_window", Desc: "Window management: raise | move | resize | release (back to layout) | close.", Props: with(map[string]any{"window": num("Window id"), "action": str("Action"), "x": num("x"), "y": num("y"), "width": num("width"), "height": num("height")}), Required: []string{"window", "action"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "POST", fmt.Sprintf("/v1/workspaces/%s/windows/%d", ws, int(a.N("window"))), pick(a, "action", "x", "y", "width", "height"))
			}},
		{Name: "desktop_launch", Desc: "Launch a GUI or CLI program in the workspace (runs unprivileged, DISPLAY set). Use restore=true to relaunch it on resume.", Props: with(map[string]any{"command": str("Executable"), "args": strs("Arguments"), "restore": boo("Relaunch on resume")}), Required: []string{"command"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/apps", pick(a, "command", "args", "restore"))
			}},
		{Name: "desktop_clipboard", Desc: "Get the clipboard, or set it when text is given.", Props: with(map[string]any{"text": str("Text to set")}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				if _, ok := a["text"]; ok {
					return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/clipboard", map[string]any{"text": a.S("text")})
				}
				return s.call(ctx, "GET", "/v1/workspaces/"+ws+"/clipboard", nil)
			}},
		{Name: "browser_open", Desc: "Open a browser tab you own, in its own window (shares the workspace login profile unless isolated=true).", Props: with(map[string]any{"url": str("URL"), "isolated": boo("Use a separate incognito-like context"), "same_window": boo("Open as a tab in an existing window")}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				b := pick(a, "url", "isolated", "same_window")
				b["observe"] = true
				var out map[string]any
				if err := s.c.Do(ctx, "POST", "/v1/workspaces/"+ws+"/tabs", b, &out); err != nil {
					return nil, err
				}
				if obs, ok := out["observation"].(string); ok {
					return text(fmt.Sprintf("target: %v\nnow visible:\n%s", out["target"], obs)), nil
				}
				return textJSON(out), nil
			}},
		{Name: "browser_tabs", Desc: "List browser tabs with owners.", Props: with(map[string]any{}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "GET", "/v1/workspaces/"+ws+"/tabs", nil)
			}},
		{Name: "browser_navigate", Desc: "Navigate a tab, wait for load and return what is visible (action tools return the visible page, so you rarely need browser_snapshot after them).", Props: tabProp(map[string]any{"url": str("URL"), "wait": str("load|domcontentloaded|none")}), Required: []string{"target", "url"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, map[string]any{"action": "navigate", "url": a.S("url"), "wait": a.S("wait")})
			}},
		{Name: "browser_snapshot", Desc: "Full semantic view of the whole page (also offscreen): interactive elements with refs [eN], roles and names, plus page text. Use refs with browser_click/browser_fill.", Props: tabProp(map[string]any{"max_text": num("Max visible text chars (default 3000)"), "boxes": boo("Also list each element's viewport box @x,y wxh in CSS pixels; after this, browser_click/hover/drag/scroll take CSS viewport pixels until the next browser_screenshot")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, map[string]any{"action": "snapshot", "max_text": a.N("max_text"), "boxes": a.B("boxes")})
			}},
		{Name: "browser_click", Desc: "Click an element by ref, or at x,y (for canvases, cross-origin iframes, CAPTCHA widgets). " + tabCoords, Props: tabProp(map[string]any{"ref": str("Element ref"), "x": num("viewport x"), "y": num("viewport y"), "button": str("left|right|middle"), "count": num("click count")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "x", "y", "button", "count")
				b["action"] = "click"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_hover", Desc: "Hover an element by ref or coordinates. " + tabCoords, Props: tabProp(map[string]any{"ref": str("Element ref"), "x": num("x"), "y": num("y")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "x", "y")
				b["action"] = "hover"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_drag", Desc: "Drag inside the page from (x,y) to (x2,y2). " + tabCoords, Props: tabProp(map[string]any{"x": num("x"), "y": num("y"), "x2": num("x2"), "y2": num("y2")}), Required: []string{"target", "x", "y", "x2", "y2"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "x", "y", "x2", "y2")
				b["action"] = "drag"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_fill", Desc: "Replace the value of a text field (by ref). submit=true presses Enter.", Props: tabProp(map[string]any{"ref": str("Element ref"), "text": str("Value"), "submit": boo("Press Enter after")}), Required: []string{"target", "ref", "text"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "text", "submit")
				b["action"] = "fill"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_type", Desc: "Type text key by key into the focused element (or click ref first). Works on background tabs without stealing anyone's focus.", Props: tabProp(map[string]any{"text": str("Text"), "ref": str("Optional element to click first")}), Required: []string{"target", "text"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "text")
				b["action"] = "type"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_press", Desc: "Press a key or combo in the tab (Enter, Tab, Escape, ctrl+a, ArrowDown...).", Props: tabProp(map[string]any{"keys": str("Combo")}), Required: []string{"target", "keys"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, map[string]any{"action": "press", "keys": a.S("keys")})
			}},
		{Name: "browser_scroll", Desc: "Scroll the page by pixels (dy>0 down). " + tabCoords, Props: tabProp(map[string]any{"dx": num("dx px"), "dy": num("dy px"), "x": num("at x"), "y": num("at y")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "dx", "dy", "x", "y")
				b["action"] = "scroll"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_select", Desc: "Select option(s) of a <select> by value or label.", Props: tabProp(map[string]any{"ref": str("Element ref"), "values": strs("Values or labels")}), Required: []string{"target", "ref", "values"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "values")
				b["action"] = "select"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_upload", Desc: "Set files on a file input (by ref) or on the file chooser that just opened (no ref). Files must be inside the workspace home (e.g. Downloads).", Props: tabProp(map[string]any{"ref": str("File input ref"), "files": strs("Paths inside the workspace home, absolute or relative to it (e.g. Downloads/report.pdf)")}), Required: []string{"target", "files"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "files")
				b["action"] = "upload"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_eval", Desc: "Evaluate a JavaScript expression in the page and return the JSON value (cut at 64 KB).", Props: tabProp(map[string]any{"expression": str("JS expression (promises are awaited)")}), Required: []string{"target", "expression"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				out, err := s.tab(ctx, a, map[string]any{"action": "eval", "expression": a.S("expression")})
				return capText(out, evalMax), err
			}},
		{Name: "browser_wait", Desc: "Wait until a selector exists, text appears, or URL contains a string.", Props: tabProp(map[string]any{"selector": str("CSS selector"), "text": str("Visible text"), "url_contains": str("URL fragment"), "timeout_ms": num("Timeout (default 15000)")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "selector", "text", "url_contains", "timeout_ms")
				b["action"] = "wait"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_screenshot", Desc: "Screenshot of a tab's viewport (or full page). Use max_width (e.g. 800) to save tokens.", Props: tabProp(map[string]any{"full_page": boo("Full page"), "format": str("jpeg|png"), "max_width": num("Downscale to this width"), "quality": num("JPEG quality")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "full_page", "format", "max_width", "quality")
				if _, ok := b["max_width"]; !ok {
					b["max_width"] = 1024
				}
				if _, ok := b["quality"]; !ok {
					b["quality"] = 60
				}
				b["action"] = "screenshot"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_audit", Desc: "One-call UI/quality audit of the current page: horizontal overflow, low-contrast text, tiny text, small tap targets, unnamed controls, unlabeled inputs, images without alt, headings outline, meta/SEO tags, performance (TTFB, LCP, CLS, bytes by type, third parties, fonts), running animations, reduced-motion support and broken internal links. Prefer it over many screenshots.", Props: tabProp(map[string]any{}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, map[string]any{"action": "audit", "timeout_ms": 30000})
			}},
		{Name: "browser_emulate", Desc: "Emulate a device viewport in the tab (layout at width x height, optional mobile + touch). Call with width=0 to reset.", Props: tabProp(map[string]any{"width": num("CSS width (e.g. 390 or 1440)"), "height": num("CSS height"), "scale": num("device pixel ratio"), "mobile": boo("mobile viewport + touch")}), Required: []string{"target", "width", "height"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "width", "height", "scale", "mobile")
				b["action"] = "emulate"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_action", Desc: "Other tab actions: back | forward | reload | dialog (accept, text) | challenge (detect CAPTCHA) | focus | info | close.", Props: tabProp(map[string]any{"action": str("Action"), "accept": boo("dialog: accept"), "text": str("dialog prompt text")}), Required: []string{"target", "action"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, pick(a, "action", "accept", "text"))
			}},
		{Name: "cursor_spawn", Desc: "Create a subagent cursor under you. With launch.adapter = claude Agora starts the agent process with its own scoped token (codex and command are admin-only); otherwise you get the token to hand to an agent yourself.", Props: map[string]any{"name": str("Name"), "goal": str("Task for the subagent"), "workspace": str("Workspace (default yours)"), "permissions": strs("Subset of: desktop, browser, process, spawn, message, workspace"), "launch": anyv("{adapter: 'claude'|'codex'|'command', prompt?, command?: [..], model?, effort?: low|medium|high, tools?: [browser|browserx|desktop|spawn|workspace|process], profile?: lean|full}"), "worktree": anyv("{repo, branch?, base?} to give it its own git worktree and port range"), "max_children": num("Max direct children it may spawn")}, Required: []string{"goal"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				body := pick(a, "name", "goal", "workspace", "permissions", "launch", "worktree")
				if n := a.N("max_children"); n > 0 {
					body["budget"] = map[string]any{"max_cursors": n}
				}
				return s.cursors(ctx, "POST", "/v1/cursors", body, 0)
			}},
		{Name: "cursor_list", Desc: "List cursors (subagents) with status and a result preview; cursor_get has the full result. Filter by parent or active.", Props: map[string]any{"parent": str("Parent cursor id"), "workspace": str("Workspace"), "active": boo("Only non-terminal")},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				act := ""
				if a.B("active") {
					act = "1"
				}
				return s.cursors(ctx, "GET", "/v1/cursors"+q("parent", a.S("parent"), "workspace", a.S("workspace"), "active", act), nil, listResultMax)
			}},
		{Name: "cursor_get", Desc: "Get one cursor (status, progress, result). Use 'self' for yourself.", Props: map[string]any{"id": str("Cursor id or self")}, Required: []string{"id"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.cursors(ctx, "GET", "/v1/cursors/"+a.S("id"), nil, -1)
			}},
		{Name: "cursor_status", Desc: "Set your (or a child's) live status: running | thinking | executing | waiting | blocked | needs_human.", Props: map[string]any{"id": str("Cursor id (default self)"), "status": str("Status"), "detail": str("Why")}, Required: []string{"status"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/cursors/"+defID(a)+"/status", pick(a, "status", "detail"))
			}},
		{Name: "cursor_report", Desc: "Report progress to your parent.", Props: map[string]any{"id": str("Cursor id (default self)"), "message": str("Progress message"), "percent": num("0-100")}, Required: []string{"message"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/cursors/"+defID(a)+"/report", pick(a, "message", "percent"))
			}},
		{Name: "cursor_complete", Desc: "Finish with a structured result (any JSON). Releases your seats, locks and owned tabs.", Props: map[string]any{"id": str("Cursor id (default self)"), "result": anyv("Result JSON")}, Required: []string{"result"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/cursors/"+defID(a)+"/complete", map[string]any{"result": a["result"]})
			}},
		{Name: "cursor_fail", Desc: "Finish with an error.", Props: map[string]any{"id": str("Cursor id (default self)"), "error": str("Error")}, Required: []string{"error"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/cursors/"+defID(a)+"/fail", pick(a, "error"))
			}},
		{Name: "cursor_control", Desc: "Control a descendant: cancel (cascades) | pause | resume | transfer (parent/workspace).", Props: map[string]any{"id": str("Cursor id"), "op": str("cancel|pause|resume|transfer"), "reason": str("Reason"), "parent": str("transfer: new parent"), "workspace": str("transfer: new workspace")}, Required: []string{"id", "op"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/cursors/"+a.S("id")+"/"+a.S("op"), pick(a, "reason", "parent", "workspace"))
			}},
		{Name: "cursor_wait", Desc: "Block until the given cursors finish (all, or any when all=false). Returns their results.", Props: map[string]any{"ids": strs("Cursor ids"), "all": boo("Wait for all (default true)"), "timeout_ms": num("Timeout (default 10 min)")}, Required: []string{"ids"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.cursors(ctx, "POST", "/v1/wait", pick(a, "ids", "all", "timeout_ms"), 0)
			}},
		{Name: "cursor_message", Desc: "Send a JSON message to another cursor's inbox.", Props: map[string]any{"to": str("Cursor id"), "body": anyv("Message body")}, Required: []string{"to", "body"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/cursors/"+a.S("to")+"/message", map[string]any{"body": a["body"]})
			}},
		{Name: "board_post", Desc: "Post to your workspace's shared board so sibling agents do not duplicate work. kind: claim (take a topic; fails if someone else holds or finished it), done (finished a topic), release (drop your claim), finding, note.", Props: with(map[string]any{"kind": str("claim|done|release|finding|note"), "topic": str("Short work item, e.g. 'audit sidebar'"), "text": str("Details")}), Required: []string{"kind"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/board", pick(a, "kind", "topic", "text"))
			}},
		{Name: "board_read", Desc: "Read the shared board: current claims and the oldest posts after seq. Check it before starting a work item. When more=true there are further posts: read again with since=seq.", Props: with(map[string]any{"since": num("Last seen seq")}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "GET", fmt.Sprintf("/v1/workspaces/%s/board?since=%d", ws, int64(a.N("since"))), nil)
			}},
		{Name: "cursor_inbox", Desc: "Read your inbox (messages after seq).", Props: map[string]any{"since": num("Last seen seq")},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "GET", fmt.Sprintf("/v1/cursors/self/inbox?since=%d", int64(a.N("since"))), nil)
			}},
		{Name: "control_surface", Desc: "Acquire or release exclusive control of a surface (win:<id>, tab:<target>) so other cursors cannot type into it.", Props: with(map[string]any{"op": str("acquire|release"), "surface": str("Surface key"), "ttl_ms": num("Lease (acquire)"), "reason": str("Why")}), Required: []string{"op", "surface"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/"+a.S("op"), pick(a, "surface", "ttl_ms", "reason"))
			}},
		{Name: "human_request", Desc: "Ask a human to take over (CAPTCHA, MFA, approval). Marks you needs_human; the browser/session stays alive. Then call events_wait with types control.returned.", Props: with(map[string]any{"reason": str("What the human must do")}), Required: []string{"reason"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/request-human", pick(a, "reason"))
			}},
		{Name: "events_wait", Desc: "Wait for events instead of polling (e.g. types=browser.page.loaded,cursor.ended,control.returned,process.exited,browser.dialog.opened,browser.challenge.detected). Returns events after 'since'.", Props: with(map[string]any{"types": str("Comma-separated types; suffix * allowed"), "cursor": str("Only events of this cursor"), "since": num("Last seen seq"), "wait_ms": num("Max wait (default 30000)")}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, _ := s.wsArg(a)
				w := a.N("wait_ms")
				if w == 0 {
					w = 30000
				}
				return s.call(ctx, "GET", "/v1/events"+q("workspace", ws, "types", a.S("types"), "cursor", a.S("cursor"), "since", fmt.Sprint(int64(a.N("since"))), "wait_ms", fmt.Sprint(int(w))), nil)
			}},
		{Name: "system_info", Desc: "Daemon resources, per-workspace memory/CPU breakdown.", Props: map[string]any{},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "GET", "/v1/system", nil)
			}},
		{Name: "worktree_create", Desc: "Create a git worktree (own branch, directory and port range) for parallel development.", Props: map[string]any{"repo": str("Path to repo"), "branch": str("Branch"), "base": str("Base ref")}, Required: []string{"repo"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/worktrees", pick(a, "repo", "branch", "base"))
			}},
	}
}

const listResultMax = 300

var cursorKeep = []string{"id", "name", "status", "detail", "parent", "goal", "result", "error", "usage", "ended"}

func slimCursor(v any, resultMax int) any {
	c, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if resultMax < 0 {
		for _, k := range []string{"token_hash", "color", "x", "y", "root", "launch_pid"} {
			delete(c, k)
		}
		if l, ok := c["launch"].(map[string]any); ok {
			delete(l, "prompt")
			delete(l, "env")
		}
		return c
	}
	out := map[string]any{}
	for _, k := range cursorKeep {
		if v, ok := c[k]; ok && v != nil && v != "" {
			out[k] = v
		}
	}
	if g, ok := out["goal"].(string); ok && len(g) > 160 {
		out["goal"] = trunc(g, 160)
	}
	if u, ok := out["usage"].(map[string]any); ok {
		out["usage"] = map[string]any{"cost_usd": u["cost_usd"], "turns": u["turns"]}
	}
	if p, ok := c["progress"].([]any); ok && len(p) > 0 {
		if last, ok := p[len(p)-1].(map[string]any); ok {
			out["last_progress"] = last["message"]
		}
	}
	if r, ok := out["result"]; ok && resultMax > 0 {
		if b, _ := json.Marshal(r); len(b) > resultMax {
			out["result"] = trunc(string(b), resultMax)
			out["result_truncated"] = true
		}
	}
	return out
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func (s *Server) cursors(ctx context.Context, method, path string, body any, resultMax int) ([]content, error) {
	var out any
	if err := s.c.Do(ctx, method, path, body, &out); err != nil {
		return nil, err
	}
	switch v := out.(type) {
	case []any:
		for i := range v {
			v[i] = slimCursor(v[i], resultMax)
		}
	case map[string]any:
		if cs, ok := v["cursors"].([]any); ok {
			for i := range cs {
				cs[i] = slimCursor(cs[i], resultMax)
			}
		} else if c, ok := v["cursor"]; ok {
			v["cursor"] = slimCursor(c, resultMax)
		} else if _, ok := v["id"]; ok {
			out = slimCursor(v, resultMax)
		}
	}
	return textJSON(out), nil
}

func defID(a Args) string {
	id := strings.TrimSpace(a.S("id"))
	if id == "" {
		return "self"
	}
	return id
}
