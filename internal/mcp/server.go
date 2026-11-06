package mcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/olimpiacloud/agora/internal/client"
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type Args map[string]any

func (a Args) S(k string) string {
	if v, ok := a[k].(string); ok {
		return v
	}
	return ""
}

func (a Args) N(k string) float64 {
	switch v := a[k].(type) {
	case float64:
		return v
	case string:
		var f float64
		fmt.Sscanf(v, "%g", &f)
		return f
	}
	return 0
}

func (a Args) B(k string) bool {
	v, _ := a[k].(bool)
	return v
}

type Tool struct {
	Name     string
	Desc     string
	Props    map[string]any
	Required []string
	Run      func(ctx context.Context, s *Server, a Args) ([]content, error)
}

type Server struct {
	c      *client.Client
	ws     string
	cursor string
	admin  bool
	once   sync.Once
	tools  []Tool
	byName map[string]*Tool
	out    io.Writer
	outMu  sync.Mutex
}

func New(c *client.Client) *Server {
	s := &Server{c: c, out: os.Stdout}
	s.tools = tools()
	s.byName = map[string]*Tool{}
	for i := range s.tools {
		s.byName[s.tools[i].Name] = &s.tools[i]
	}
	return s
}

func (s *Server) identity(ctx context.Context) {
	s.once.Do(func() {
		var me struct {
			Admin  bool `json:"admin"`
			Cursor *struct {
				ID        string `json:"id"`
				Workspace string `json:"workspace"`
			} `json:"cursor"`
		}
		if err := s.c.Do(ctx, "GET", "/v1/me", nil, &me); err == nil {
			s.admin = me.Admin
			if me.Cursor != nil {
				s.cursor = me.Cursor.ID
				s.ws = me.Cursor.Workspace
			}
		}
		if s.ws == "" {
			s.ws = os.Getenv("AGORA_WORKSPACE")
		}
	})
}

func (s *Server) wsArg(a Args) (string, error) {
	if w := a.S("workspace"); w != "" {
		return w, nil
	}
	if s.ws != "" {
		return s.ws, nil
	}
	return "", fmt.Errorf("workspace is required")
}

func (s *Server) write(v any) {
	b, _ := json.Marshal(v)
	s.outMu.Lock()
	s.out.Write(append(b, '\n'))
	s.outMu.Unlock()
}

func (s *Server) Serve(ctx context.Context, in io.Reader) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var wg sync.WaitGroup
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.write(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		wg.Add(1)
		go func(req rpcReq) {
			defer wg.Done()
			res, rerr := s.handle(ctx, req)
			msg := map[string]any{"jsonrpc": "2.0", "id": req.ID}
			if rerr != nil {
				msg["error"] = rerr
			} else {
				msg["result"] = res
			}
			s.write(msg)
		}(req)
	}
	wg.Wait()
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, req rpcReq) (any, map[string]any) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		pv := p.ProtocolVersion
		if pv == "" {
			pv = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agora", "version": "0.1.0"},
			"instructions":    "Agora gives you a real Linux desktop with Chromium. Prefer browser_* tools (semantic, refs from browser_snapshot) for web pages; use desktop_* tools (pixels + real mouse/keyboard on your own input seat) for anything else. Spawn subagents with cursor_spawn, wait with cursor_wait, and report with cursor_report/cursor_complete. If a CAPTCHA or login needs a person, call human_request and then events_wait for control.returned.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		s.identity(ctx)
		groups := toolGroups(os.Getenv("AGORA_TOOLS"))
		lean := os.Getenv("AGORA_LEAN") == "1"
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			if !groupAllowed(t.Name, groups) {
				continue
			}
			props := map[string]any{}
			for k, v := range t.Props {
				if k == "workspace" && !s.admin && s.ws != "" && t.Name != "cursor_spawn" && t.Name != "cursor_list" {
					continue
				}
				if lean && k == "target" {
					v = str("Tab target id")
				}
				props[k] = v
			}
			schema := map[string]any{"type": "object", "properties": props}
			if len(t.Required) > 0 {
				schema["required"] = t.Required
			}
			desc := t.Desc
			if lean {
				desc = firstSentence(desc)
			}
			list = append(list, map[string]any{"name": t.Name, "description": desc, "inputSchema": schema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments Args   `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		t := s.byName[p.Name]
		if t == nil {
			return nil, map[string]any{"code": -32602, "message": "unknown tool " + p.Name}
		}
		if p.Arguments == nil {
			p.Arguments = Args{}
		}
		s.identity(ctx)
		out, err := t.Run(ctx, s, p.Arguments)
		if err != nil {
			return map[string]any{"content": []content{{Type: "text", Text: "error: " + err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": out}, nil
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + req.Method}
}

func textJSON(v any) []content {
	b, _ := json.MarshalIndent(v, "", " ")
	return []content{{Type: "text", Text: string(b)}}
}

func text(s string) []content { return []content{{Type: "text", Text: s}} }

func (s *Server) call(ctx context.Context, method, path string, body any) ([]content, error) {
	var out any
	if err := s.c.Do(ctx, method, path, body, &out); err != nil {
		return nil, err
	}
	return textJSON(out), nil
}

func (s *Server) image(ctx context.Context, path string) ([]content, error) {
	b, h, err := s.c.RawHeader(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	out := []content{{Type: "image", Data: base64.StdEncoding.EncodeToString(b), MimeType: h.Get("Content-Type")}}
	if id := h.Get("X-Agora-Shot"); id != "" {
		out = append(out, content{Type: "text", Text: "agora-shot:" + id})
	}
	return out, nil
}

func q(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" && kv[i+1] != "0" {
			v.Set(kv[i], kv[i+1])
		}
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

var groupOf = map[string]string{
	"cursor_status": "self", "cursor_report": "self", "cursor_complete": "self", "cursor_fail": "self", "human_request": "self", "events_wait": "self", "cursor_inbox": "self",
	"cursor_spawn": "spawn", "cursor_list": "spawn", "cursor_get": "spawn", "cursor_wait": "spawn", "cursor_control": "spawn", "cursor_message": "spawn",
	"control_surface": "desktop", "system_info": "workspace", "worktree_create": "process",
	"browser_drag": "browserx", "browser_hover": "browserx", "browser_upload": "browserx", "browser_tabs": "browserx", "browser_wait": "browserx", "browser_emulate": "browserx", "browser_audit": "browserx",
}

func toolGroup(name string) string {
	if g, ok := groupOf[name]; ok {
		return g
	}
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i]
	}
	return name
}

func toolGroups(env string) map[string]bool {
	if strings.TrimSpace(env) == "" || env == "all" {
		return nil
	}
	m := map[string]bool{"self": true}
	for _, g := range strings.Split(env, ",") {
		m[strings.TrimSpace(g)] = true
	}
	return m
}

func groupAllowed(name string, groups map[string]bool) bool {
	return groups == nil || groups[toolGroup(name)]
}

func firstSentence(d string) string {
	if i := strings.Index(d, ". "); i > 0 && i < len(d)-2 {
		return d[:i+1]
	}
	return d
}
