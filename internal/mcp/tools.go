package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

func (s *Server) tab(ctx context.Context, a Args, body map[string]any) ([]content, error) {
	ws, err := s.wsArg(a)
	if err != nil {
		return nil, err
	}
	t := a.S("target")
	if t == "" {
		return nil, fmt.Errorf("target is required")
	}
	var out map[string]any
	if err := s.c.Do(ctx, "POST", "/v1/workspaces/"+ws+"/tabs/"+t, body, &out); err != nil {
		return nil, err
	}
	if img, ok := out["image"].(string); ok {
		return []content{{Type: "image", Data: img, MimeType: fmt.Sprint(out["mime"])}}, nil
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
		{Name: "workspace_op", Desc: "Lifecycle: start | suspend | resume | freeze | thaw | checkpoint | fork | resize | layout | browser-restart.", Props: with(map[string]any{"op": str("Operation"), "name": str("fork name"), "width": num("resize width"), "height": num("resize height"), "layout": str("tile|max|float"), "snapshot": boo("checkpoint: also snapshot browser profile")}), Required: []string{"op"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/"+a.S("op"), pick(a, "name", "width", "height", "layout", "snapshot"))
			}},
		{Name: "desktop_screenshot", Desc: "Capture the desktop (or one window / region) as an image. Coordinates in the image match desktop_* coordinates unless max_width scales it.", Props: with(map[string]any{"window": num("X window id"), "x": num("region x"), "y": num("region y"), "w": num("region width"), "h": num("region height"), "max_width": num("downscale to this width"), "format": str("jpeg|png")}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				f := func(k string) string { return fmt.Sprint(int(a.N(k))) }
				return s.image(ctx, "/v1/workspaces/"+ws+"/screenshot"+q("window", f("window"), "x", f("x"), "y", f("y"), "w", f("w"), "h", f("h"), "max_width", f("max_width"), "format", a.S("format")))
			}},
		{Name: "desktop_windows", Desc: "List top-level windows (id, title, class, geometry).", Props: with(map[string]any{}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "GET", "/v1/workspaces/"+ws+"/windows", nil)
			}},
		{Name: "desktop_click", Desc: "Click with your own pointer at screen coordinates. Focuses and raises the window under it.", Props: with(map[string]any{"x": num("x"), "y": num("y"), "button": num("1 left, 2 middle, 3 right"), "count": num("clicks (2 = double)")}), Required: []string{"x", "y"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "click", "x": a.N("x"), "y": a.N("y"), "button": a.N("button"), "count": a.N("count")})
			}},
		{Name: "desktop_move", Desc: "Move your pointer (hover).", Props: with(map[string]any{"x": num("x"), "y": num("y")}), Required: []string{"x", "y"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "move", "x": a.N("x"), "y": a.N("y")})
			}},
		{Name: "desktop_drag", Desc: "Drag with the left button from (x,y) to (x2,y2).", Props: with(map[string]any{"x": num("from x"), "y": num("from y"), "x2": num("to x"), "y2": num("to y")}), Required: []string{"x", "y", "x2", "y2"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.input(ctx, a, map[string]any{"action": "drag", "x": a.N("x"), "y": a.N("y"), "x2": a.N("x2"), "y2": a.N("y2")})
			}},
		{Name: "desktop_scroll", Desc: "Scroll at a position. dy>0 scrolls down (notches).", Props: with(map[string]any{"x": num("x"), "y": num("y"), "dx": num("horizontal notches"), "dy": num("vertical notches")}),
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
				return s.call(ctx, "POST", "/v1/workspaces/"+ws+"/tabs", pick(a, "url", "isolated", "same_window"))
			}},
		{Name: "browser_tabs", Desc: "List browser tabs with owners.", Props: with(map[string]any{}),
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				ws, err := s.wsArg(a)
				if err != nil {
					return nil, err
				}
				return s.call(ctx, "GET", "/v1/workspaces/"+ws+"/tabs", nil)
			}},
		{Name: "browser_navigate", Desc: "Navigate a tab and wait for load (wait: load | domcontentloaded | none).", Props: tabProp(map[string]any{"url": str("URL"), "wait": str("load|domcontentloaded|none")}), Required: []string{"target", "url"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, map[string]any{"action": "navigate", "url": a.S("url"), "wait": a.S("wait")})
			}},
		{Name: "browser_snapshot", Desc: "Compact semantic view of the page: interactive elements with refs [eN], roles, names, boxes, plus visible text. Use refs with browser_click/browser_fill.", Props: tabProp(map[string]any{"max_text": num("Max visible text chars (default 3000)")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, map[string]any{"action": "snapshot", "max_text": a.N("max_text")})
			}},
		{Name: "browser_click", Desc: "Click an element by ref, or at viewport coordinates x,y (for canvases, cross-origin iframes, CAPTCHA widgets).", Props: tabProp(map[string]any{"ref": str("Element ref"), "x": num("viewport x"), "y": num("viewport y"), "button": str("left|right|middle"), "count": num("click count")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "x", "y", "button", "count")
				b["action"] = "click"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_hover", Desc: "Hover an element by ref or coordinates.", Props: tabProp(map[string]any{"ref": str("Element ref"), "x": num("x"), "y": num("y")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "x", "y")
				b["action"] = "hover"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_drag", Desc: "Drag inside the page from (x,y) to (x2,y2) in viewport coordinates.", Props: tabProp(map[string]any{"x": num("x"), "y": num("y"), "x2": num("x2"), "y2": num("y2")}), Required: []string{"target", "x", "y", "x2", "y2"},
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
		{Name: "browser_scroll", Desc: "Scroll the page by pixels (dy>0 down).", Props: tabProp(map[string]any{"dx": num("dx px"), "dy": num("dy px"), "x": num("at x"), "y": num("at y")}), Required: []string{"target"},
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
		{Name: "browser_upload", Desc: "Set files on a file input (by ref) or on the file chooser that just opened (no ref). Paths are on the Carbon host.", Props: tabProp(map[string]any{"ref": str("File input ref"), "files": strs("Absolute paths")}), Required: []string{"target", "files"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "ref", "files")
				b["action"] = "upload"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_eval", Desc: "Evaluate a JavaScript expression in the page and return the JSON value.", Props: tabProp(map[string]any{"expression": str("JS expression (promises are awaited)")}), Required: []string{"target", "expression"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, map[string]any{"action": "eval", "expression": a.S("expression")})
			}},
		{Name: "browser_wait", Desc: "Wait until a selector exists, text appears, or URL contains a string.", Props: tabProp(map[string]any{"selector": str("CSS selector"), "text": str("Visible text"), "url_contains": str("URL fragment"), "timeout_ms": num("Timeout (default 15000)")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "selector", "text", "url_contains", "timeout_ms")
				b["action"] = "wait"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_screenshot", Desc: "Screenshot of a tab's viewport (or full page).", Props: tabProp(map[string]any{"full_page": boo("Full page"), "format": str("jpeg|png")}), Required: []string{"target"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				b := pick(a, "full_page", "format")
				b["action"] = "screenshot"
				return s.tab(ctx, a, b)
			}},
		{Name: "browser_action", Desc: "Other tab actions: back | forward | reload | dialog (accept, text) | challenge (detect CAPTCHA) | focus | info | close.", Props: tabProp(map[string]any{"action": str("Action"), "accept": boo("dialog: accept"), "text": str("dialog prompt text")}), Required: []string{"target", "action"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.tab(ctx, a, pick(a, "action", "accept", "text"))
			}},
		{Name: "cursor_spawn", Desc: "Create a subagent cursor under you. With launch.adapter = claude | codex | command Carbon starts the agent process with its own scoped token; otherwise you get the token to hand to an agent yourself.", Props: map[string]any{"name": str("Name"), "goal": str("Task for the subagent"), "workspace": str("Workspace (default yours)"), "permissions": strs("Subset of: desktop, browser, process, spawn, message, workspace"), "launch": anyv("{adapter: 'claude'|'codex'|'command', prompt?, command?: [..], model?}"), "worktree": anyv("{repo, branch?, base?} to give it its own git worktree and port range"), "max_children": num("Max direct children it may spawn")}, Required: []string{"goal"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				body := pick(a, "name", "goal", "workspace", "permissions", "launch", "worktree")
				if n := a.N("max_children"); n > 0 {
					body["budget"] = map[string]any{"max_cursors": n}
				}
				return s.call(ctx, "POST", "/v1/cursors", body)
			}},
		{Name: "cursor_list", Desc: "List cursors (subagents) with status and results. Filter by parent or active.", Props: map[string]any{"parent": str("Parent cursor id"), "workspace": str("Workspace"), "active": boo("Only non-terminal")},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				act := ""
				if a.B("active") {
					act = "1"
				}
				return s.call(ctx, "GET", "/v1/cursors"+q("parent", a.S("parent"), "workspace", a.S("workspace"), "active", act), nil)
			}},
		{Name: "cursor_get", Desc: "Get one cursor (status, progress, result). Use 'self' for yourself.", Props: map[string]any{"id": str("Cursor id or self")}, Required: []string{"id"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "GET", "/v1/cursors/"+a.S("id"), nil)
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
				return s.call(ctx, "POST", "/v1/wait", pick(a, "ids", "all", "timeout_ms"))
			}},
		{Name: "cursor_message", Desc: "Send a JSON message to another cursor's inbox.", Props: map[string]any{"to": str("Cursor id"), "body": anyv("Message body")}, Required: []string{"to", "body"},
			Run: func(ctx context.Context, s *Server, a Args) ([]content, error) {
				return s.call(ctx, "POST", "/v1/cursors/"+a.S("to")+"/message", map[string]any{"body": a["body"]})
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

func defID(a Args) string {
	id := strings.TrimSpace(a.S("id"))
	if id == "" {
		return "self"
	}
	return id
}
