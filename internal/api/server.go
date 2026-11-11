package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/olimpiacloud/agora/internal/core"
)

type Server struct {
	m              *core.Manager
	auth           *Auth
	mux            *http.ServeMux
	ui             fs.FS
	wsMu           sync.Mutex
	sandboxDefault func() bool
}

type ctxKey struct{}

func New(m *core.Manager, auth *Auth, ui fs.FS) *Server {
	s := &Server{m: m, auth: auth, mux: http.NewServeMux(), ui: ui, sandboxDefault: m.SandboxDefault}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.mux.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	var ce *core.ConflictError
	var cc *core.ClaimConflict
	switch {
	case errors.As(err, &cc):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "claim": cc.Claim})
		return
	case errors.Is(err, core.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, core.ErrForbidden):
		code = http.StatusForbidden
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "lock": ce.Lock})
		return
	case errors.Is(err, context.DeadlineExceeded):
		code = http.StatusGatewayTimeout
	}
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func decode(r *http.Request, vs ...any) error {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	for _, v := range vs {
		if err := json.Unmarshal(b, v); err != nil {
			return err
		}
	}
	return nil
}

func principal(r *http.Request) *Principal {
	p, _ := r.Context().Value(ctxKey{}).(*Principal)
	return p
}

type handler func(w http.ResponseWriter, r *http.Request, p *Principal)

func (s *Server) h(f handler, query bool) http.HandlerFunc {
	return s.handle(f, query, query)
}

func (s *Server) handle(f handler, query, viewer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.auth.Resolve(r, query)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
			return
		}
		if p.Viewer != nil && !viewer {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "viewer token only valid for streaming"})
			return
		}
		f(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)), p)
	}
}

func (s *Server) admin(f handler) http.HandlerFunc {
	return s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		if !p.Admin {
			writeErr(w, core.ErrForbidden)
			return
		}
		f(w, r, p)
	}, false)
}

func (s *Server) ws(f func(w http.ResponseWriter, r *http.Request, p *Principal, ws string)) http.HandlerFunc {
	return s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		id := r.PathValue("id")
		if !p.CanWorkspace(id) {
			writeErr(w, fmt.Errorf("%w: no access to workspace %s", core.ErrForbidden, id))
			return
		}
		f(w, r, p, id)
	}, false)
}

func (s *Server) cur(f func(w http.ResponseWriter, r *http.Request, p *Principal, id string)) http.HandlerFunc {
	return s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		id := r.PathValue("id")
		if id == "self" && p.Cursor != nil {
			id = p.Cursor.ID
		}
		if !p.Admin && (p.Cursor == nil || !s.m.IsAncestor(p.Cursor.ID, id)) {
			writeErr(w, fmt.Errorf("%w: cursor %s is not yours", core.ErrForbidden, id))
			return
		}
		f(w, r, p, id)
	}, false)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": core.Version})
	})
	m.HandleFunc("GET /v1/system", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		sys := s.m.System()
		if !p.Admin {
			for id := range sys.Usage {
				if !p.CanWorkspace(id) {
					delete(sys.Usage, id)
				}
			}
		}
		writeJSON(w, 200, sys)
	}, false))
	m.HandleFunc("GET /v1/me", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		writeJSON(w, 200, map[string]any{"admin": p.Admin, "cursor": p.Cursor.Public()})
	}, false))
	m.HandleFunc("POST /v1/viewer-tokens", s.admin(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		var req struct {
			Workspace string `json:"workspace"`
			Control   bool   `json:"control"`
			TTL       int    `json:"ttl_seconds"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		if req.TTL <= 0 || req.TTL > 86400 {
			req.TTL = 3600
		}
		writeJSON(w, 200, map[string]any{"token": s.auth.MintViewer(req.Workspace, req.Control, time.Duration(req.TTL)*time.Second), "expires_in": req.TTL})
	}))

	m.HandleFunc("POST /v1/workspaces", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		if !p.Admin && p.Cursor == nil {
			writeErr(w, core.ErrForbidden)
			return
		}
		if err := need(p, "workspace"); err != nil {
			writeErr(w, err)
			return
		}
		var req core.CreateWorkspaceReq
		var opt struct {
			Config struct {
				Sandbox *bool `json:"sandbox"`
			} `json:"config"`
		}
		if err := decode(r, &req, &opt); err != nil {
			writeErr(w, err)
			return
		}
		req.Config.Sandbox = sandboxed(opt.Config.Sandbox, !p.limited(), s.sandboxDefault())
		if p.Cursor != nil {
			req.Owner = p.Cursor.ID
		}
		var ws *core.Workspace
		var err error
		if p.limited() {
			if len(req.Config.BrowserArgs) > 0 || req.Config.Profile != "" {
				writeErr(w, fmt.Errorf("%w: only the admin can set browser_args or profile", core.ErrForbidden))
				return
			}
			ws, err = s.createCursorWorkspace(r.Context(), p.Cursor, req)
		} else {
			ws, err = s.m.CreateWorkspace(r.Context(), req)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, ws)
	}, false))
	m.HandleFunc("GET /v1/workspaces", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		all := s.m.ListWorkspaces()
		out := []*core.WorkspaceView{}
		for _, v := range all {
			if p.CanWorkspace(v.ID) {
				out = append(out, v)
			}
		}
		writeJSON(w, 200, out)
	}, false))
	m.HandleFunc("GET /v1/workspaces/{id}", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if r.URL.Query().Get("detail") == "1" {
			s.m.DetailedUsage(id)
		}
		v, err := s.m.ViewWorkspace(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, v)
	}))
	m.HandleFunc("DELETE /v1/workspaces/{id}", s.admin(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		purge := r.URL.Query().Get("purge") == "1" || r.URL.Query().Get("purge") == "true"
		if err := s.m.Destroy(r.Context(), r.PathValue("id"), purge); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "purged": purge})
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/{op}", s.ws(s.workspaceOp))
	m.HandleFunc("GET /v1/workspaces/{id}/windows", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if err := need(p, "desktop"); err != nil {
			writeErr(w, err)
			return
		}
		ws, err := s.m.Windows(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, ws)
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/windows/{win}", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		win, _ := strconv.ParseUint(r.PathValue("win"), 10, 32)
		var req core.WindowReq
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.m.WindowOp(r.Context(), id, p.CursorID(), uint32(win), req); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/screenshot", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if err := need(p, "desktop"); err != nil {
			writeErr(w, err)
			return
		}
		q := r.URL.Query()
		atoi := func(k string) int { v, _ := strconv.Atoi(q.Get(k)); return v }
		win, _ := strconv.ParseUint(q.Get("window"), 10, 32)
		b, info, err := s.m.Screenshot(r.Context(), id, core.ShotOpts{Window: uint32(win), X: atoi("x"), Y: atoi("y"), W: atoi("w"), H: atoi("h"), Format: q.Get("format"), Quality: atoi("quality"), MaxWidth: atoi("max_width")})
		if err != nil {
			writeErr(w, err)
			return
		}
		mime := info.Mime
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Agora-Region", fmt.Sprintf("%d,%d,%d,%d", info.X, info.Y, info.W, info.H))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Agora-Shot", s.m.StoreShot(id, p.CursorID(), mime, b))
		w.Write(b)
	}))
	m.HandleFunc("GET /v1/shots/{sid}", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		sh := s.m.GetShot(r.PathValue("sid"))
		if sh == nil || !p.CanWorkspace(sh.Workspace) {
			writeErr(w, core.ErrNotFound)
			return
		}
		if p.limited() && !p.Cursor.Can("desktop") && !p.Cursor.Can("browser") {
			writeErr(w, fmt.Errorf("%w: needs desktop or browser permission", core.ErrForbidden))
			return
		}
		w.Header().Set("Content-Type", sh.Mime)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Write(sh.Data)
	}, false))
	m.HandleFunc("POST /v1/workspaces/{id}/input", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		var req core.InputReq
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		res, err := s.m.DesktopInput(r.Context(), id, p.CursorID(), req)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, res)
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/apps", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if err := need(p, "process"); err != nil {
			writeErr(w, err)
			return
		}
		var spec core.AppSpec
		if err := decode(r, &spec); err != nil {
			writeErr(w, err)
			return
		}
		spec.Owner = p.CursorID()
		pr, err := s.m.LaunchApp(r.Context(), id, spec)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, pr)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/processes", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if err := need(p, "process"); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, s.m.Processes(id))
	}))
	m.HandleFunc("DELETE /v1/workspaces/{id}/processes/{pid}", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		pid, _ := strconv.Atoi(r.PathValue("pid"))
		if err := s.canKill(p, id, pid); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.m.KillProcess(id, pid); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/clipboard", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if err := need(p, "desktop"); err != nil {
			writeErr(w, err)
			return
		}
		t, err := s.m.Clipboard(r.Context(), id, nil)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"text": t})
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/tabs", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if err := need(p, "browser"); err != nil {
			writeErr(w, err)
			return
		}
		t, err := s.m.ListTabs(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, t)
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/tabs", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		var req core.OpenTabReq
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		res, err := s.m.OpenTab(r.Context(), id, p.CursorID(), req)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, res)
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/tabs/{target}", s.ws(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		var a core.TabAction
		if err := decode(r, &a); err != nil {
			writeErr(w, err)
			return
		}
		ctx := r.Context()
		if a.TimeoutMs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(a.TimeoutMs+5000)*time.Millisecond)
			defer cancel()
		} else {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
		}
		res, err := s.m.TabDo(ctx, id, p.CursorID(), r.PathValue("target"), a)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, res)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/board", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		id := r.PathValue("id")
		if !p.CanWorkspace(id) && !s.ownsWorkspace(p, id) {
			writeErr(w, core.ErrForbidden)
			return
		}
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		writeJSON(w, 200, s.m.BoardRead(id, since, limit))
	}, false))
	m.HandleFunc("POST /v1/workspaces/{id}/board", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		id := r.PathValue("id")
		if !p.CanWorkspace(id) && !s.ownsWorkspace(p, id) {
			writeErr(w, core.ErrForbidden)
			return
		}
		if err := need(p, "message"); err != nil {
			writeErr(w, err)
			return
		}
		var req struct {
			Kind  string `json:"kind"`
			Topic string `json:"topic"`
			Text  string `json:"text"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		post, err := s.m.BoardPost(id, p.CursorID(), req.Kind, req.Topic, req.Text)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, post)
	}, false))
	m.HandleFunc("GET /v1/workspaces/{id}/locks", s.handle(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		id := r.PathValue("id")
		if !p.CanWorkspace(id) {
			writeErr(w, fmt.Errorf("%w: no access to workspace %s", core.ErrForbidden, id))
			return
		}
		writeJSON(w, 200, s.m.Arb.List(id))
	}, false, true))
	m.HandleFunc("GET /v1/workspaces/{id}/stream/ws", s.h(s.streamWS, true))
	m.HandleFunc("POST /v1/workspaces/{id}/stream/webrtc", s.h(s.streamRTC, true))
	m.HandleFunc("GET /v1/workspaces/{id}/stream/jpeg", s.h(s.streamJPEG, true))

	m.HandleFunc("POST /v1/cursors", s.h(s.createCursor, false))
	m.HandleFunc("GET /v1/cursors", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		q := r.URL.Query()
		cq := core.CursorQuery{Workspace: q.Get("workspace"), Parent: q.Get("parent"), Root: q.Get("root"), Active: q.Get("active") == "1" || q.Get("active") == "true"}
		all := s.m.ListCursors(cq)
		out := []*core.Cursor{}
		for _, c := range all {
			if p.Admin || (p.Cursor != nil && (c.Workspace != "" && c.Workspace == p.Cursor.Workspace || s.m.IsAncestor(p.Cursor.ID, c.ID))) {
				out = append(out, c.Public())
			}
		}
		writeJSON(w, 200, out)
	}, false))
	m.HandleFunc("GET /v1/cursors/{id}", s.cur(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		c, err := s.m.GetCursor(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, c.Public())
	}))
	m.HandleFunc("GET /v1/cursors/{id}/inbox", s.cur(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		writeJSON(w, 200, s.m.Inbox(id, since))
	}))
	m.HandleFunc("DELETE /v1/cursors/{id}", s.cur(func(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
		if err := s.m.DeleteCursor(id); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	}))
	m.HandleFunc("POST /v1/cursors/{id}/{op}", s.h(s.cursorOp, false))
	m.HandleFunc("POST /v1/wait", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		var req struct {
			IDs       []string `json:"ids"`
			All       *bool    `json:"all"`
			TimeoutMs int      `json:"timeout_ms"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		for _, id := range req.IDs {
			if !p.Admin && (p.Cursor == nil || !s.m.IsAncestor(p.Cursor.ID, id)) {
				writeErr(w, core.ErrForbidden)
				return
			}
		}
		all := req.All == nil || *req.All
		cs, err := s.m.WaitCursors(r.Context(), req.IDs, all, time.Duration(req.TimeoutMs)*time.Millisecond)
		res := map[string]any{"cursors": core.PublicCursors(cs), "timeout": err != nil}
		writeJSON(w, 200, res)
	}, false))

	m.HandleFunc("GET /v1/events", s.h(s.pollEvents, false))
	m.HandleFunc("GET /v1/events/stream", s.h(s.sseEvents, true))

	m.HandleFunc("POST /v1/worktrees", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		var req struct {
			core.WorktreeReq
			Cursor    string `json:"cursor"`
			Workspace string `json:"workspace"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		if !p.Admin {
			if p.Cursor == nil || !p.Cursor.Can("process") {
				writeErr(w, core.ErrForbidden)
				return
			}
			req.Cursor = p.Cursor.ID
			req.Workspace = p.Cursor.Workspace
		}
		req.Trusted = p.Admin
		wt, err := s.m.CreateWorktree(r.Context(), req.WorktreeReq, req.Cursor, req.Workspace)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, wt)
	}, false))
	m.HandleFunc("GET /v1/worktrees", s.h(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		out := []*core.Worktree{}
		for _, wt := range s.m.ListWorktrees() {
			if p.Admin || (p.Cursor != nil && (wt.Workspace == p.Cursor.Workspace && wt.Workspace != "" || s.m.IsAncestor(p.Cursor.ID, wt.Cursor))) {
				out = append(out, wt)
			}
		}
		writeJSON(w, 200, out)
	}, false))
	m.HandleFunc("DELETE /v1/worktrees/{id}", s.admin(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		if err := s.m.RemoveWorktree(r.Context(), r.PathValue("id"), r.URL.Query().Get("force") == "1"); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	}))

	if s.ui != nil {
		files := http.FileServer(http.FS(s.ui))
		m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' ws: wss:; style-src 'self' 'unsafe-inline'")
			if _, err := fs.Stat(s.ui, strings.TrimPrefix(r.URL.Path, "/")); err != nil || r.URL.Path == "/" {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				files.ServeHTTP(w, r2)
				return
			}
			files.ServeHTTP(w, r)
		})
	}
}

func (s *Server) workspaceOp(w http.ResponseWriter, r *http.Request, p *Principal, id string) {
	op := r.PathValue("op")
	ctx := r.Context()
	var body struct {
		Name     string  `json:"name"`
		Width    int     `json:"width"`
		Height   int     `json:"height"`
		Layout   string  `json:"layout"`
		Snapshot bool    `json:"snapshot"`
		Text     *string `json:"text"`
		Reason   string  `json:"reason"`
		Surface  string  `json:"surface"`
		TTLms    int     `json:"ttl_ms"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	privileged := map[string]bool{"suspend": true, "freeze": true, "checkpoint": true, "fork": true, "resize": true, "layout": true, "browser-restart": true}
	if (op == "control-take" || op == "control-return") && !p.Admin {
		writeErr(w, fmt.Errorf("%w: only a human (the admin) can take or return control; call human_request instead", core.ErrForbidden))
		return
	}
	if privileged[op] {
		if err := need(p, "workspace"); err != nil {
			writeErr(w, err)
			return
		}
	}
	if op == "clipboard" {
		if err := need(p, "desktop"); err != nil {
			writeErr(w, err)
			return
		}
	}
	if op == "acquire" {
		if err := acquireAllowed(p, body.Surface); err != nil {
			writeErr(w, err)
			return
		}
	}
	var err error
	var res any = map[string]any{"ok": true}
	switch op {
	case "start":
		err = s.m.Start(ctx, id)
	case "suspend":
		err = s.m.Suspend(ctx, id, cmp.Or(body.Reason, "api"))
	case "resume":
		err = s.m.Resume(ctx, id)
	case "freeze":
		err = s.m.Freeze(ctx, id)
	case "thaw":
		err = s.m.Thaw(ctx, id)
	case "checkpoint":
		res, err = s.m.Checkpoint(ctx, id, cmp.Or(body.Reason, "api"), body.Snapshot)
	case "fork":
		res, err = s.m.Fork(ctx, id, body.Name)
	case "resize":
		err = s.m.Resize(ctx, id, body.Width, body.Height)
	case "layout":
		err = s.m.SetLayout(ctx, id, body.Layout)
	case "clipboard":
		var t string
		t, err = s.m.Clipboard(ctx, id, body.Text)
		res = map[string]any{"text": t}
	case "browser-restart":
		err = s.m.BrowserRestart(ctx, id)
	case "request-human":
		err = s.m.RequestHuman(id, p.CursorID(), body.Reason)
	case "control-take":
		holder := "human:api"
		if p.Cursor != nil {
			holder = p.Cursor.ID
		}
		res, err = s.m.TakeControl(id, holder)
	case "control-return":
		holder := "human:api"
		if p.Cursor != nil {
			holder = p.Cursor.ID
		}
		res = map[string]any{"released": s.m.ReturnControl(id, holder)}
	case "acquire":
		res, err = s.m.AcquireSurface(id, p.CursorID(), body.Surface, time.Duration(body.TTLms)*time.Millisecond, body.Reason)
	case "release":
		res = map[string]any{"released": s.m.ReleaseSurface(id, p.CursorID(), body.Surface)}
	case "usage":
		res = s.m.DetailedUsage(id)
	default:
		err = fmt.Errorf("%w: unknown op %q", core.ErrNotFound, op)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) createCursor(w http.ResponseWriter, r *http.Request, p *Principal) {
	var req core.CreateCursorReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if !p.Admin {
		if p.Cursor == nil || !p.Cursor.Can("spawn") {
			writeErr(w, fmt.Errorf("%w: needs spawn permission", core.ErrForbidden))
			return
		}
		if req.Parent == "" || !s.m.IsAncestor(p.Cursor.ID, req.Parent) {
			req.Parent = p.Cursor.ID
		}
		if req.Workspace != "" && req.Workspace != p.Cursor.Workspace && !s.ownsWorkspace(p, req.Workspace) {
			writeErr(w, fmt.Errorf("%w: cannot place subagent in a workspace you did not create", core.ErrForbidden))
			return
		}
		if req.Launch != nil && req.Launch.Adapter == "codex" {
			writeErr(w, fmt.Errorf("%w: codex subagents keep their built-in shell, so only the admin can launch them; use the claude adapter", core.ErrForbidden))
			return
		}
		if req.Launch != nil && (req.Launch.Adapter != "claude" && req.Launch.Adapter != "claude-code" || len(req.Launch.Command) > 0 || len(req.Launch.Env) > 0) {
			writeErr(w, fmt.Errorf("%w: subagents may only be launched with the claude adapter, without command or env", core.ErrForbidden))
			return
		}
		if req.Launch != nil && req.Launch.Profile == "full" {
			writeErr(w, fmt.Errorf("%w: only the admin can launch subagents with the full profile", core.ErrForbidden))
			return
		}
		if req.Worktree != nil && !p.Cursor.Can("process") {
			writeErr(w, fmt.Errorf("%w: a worktree needs process permission", core.ErrForbidden))
			return
		}
	}
	if req.Worktree != nil {
		req.Worktree.Trusted = p.Admin
	}
	c, tok, err := s.m.CreateCursor(r.Context(), req)
	if err != nil && c == nil {
		writeErr(w, err)
		return
	}
	res := map[string]any{"cursor": c.Public(), "token": tok}
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, 201, res)
}

func (s *Server) cursorOp(w http.ResponseWriter, r *http.Request, p *Principal) {
	id := r.PathValue("id")
	if id == "self" && p.Cursor != nil {
		id = p.Cursor.ID
	}
	op := r.PathValue("op")
	isSelf := p.Cursor != nil && p.Cursor.ID == id
	ancestor := p.Cursor != nil && s.m.IsAncestor(p.Cursor.ID, id)
	if op == "message" {
		if err := need(p, "message"); err != nil {
			writeErr(w, err)
			return
		}
		if !p.Admin && !s.canMessage(p, id) {
			writeErr(w, fmt.Errorf("%w: cursor %s is outside your workspace and tree", core.ErrForbidden, id))
			return
		}
	} else if !p.Admin && !ancestor {
		writeErr(w, fmt.Errorf("%w: cursor %s is not yours", core.ErrForbidden, id))
		return
	}
	var body struct {
		Status    core.CursorStatus `json:"status"`
		Detail    string            `json:"detail"`
		Message   string            `json:"message"`
		Percent   int               `json:"percent"`
		Result    json.RawMessage   `json:"result"`
		Error     string            `json:"error"`
		Reason    string            `json:"reason"`
		Cascade   *bool             `json:"cascade"`
		Body      json.RawMessage   `json:"body"`
		TimeoutMs int               `json:"timeout_ms"`
		core.TransferReq
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	var err error
	var res any = map[string]any{"ok": true}
	switch op {
	case "status":
		err = s.m.SetStatus(id, body.Status, body.Detail)
	case "report":
		err = s.m.Report(id, body.Message, body.Percent)
	case "complete":
		if len(body.Result) == 0 {
			body.Result = json.RawMessage(`null`)
		}
		err = s.m.Complete(id, body.Result)
	case "fail":
		err = s.m.Fail(id, cmp.Or(body.Error, body.Message))
	case "cancel":
		cascade := body.Cascade == nil || *body.Cascade
		if isSelf && !p.Admin {
			err = s.m.CancelCursor(id, cmp.Or(body.Reason, "self-cancel"), cascade)
		} else {
			err = s.m.CancelCursor(id, cmp.Or(body.Reason, "cancelled by "+cmp.Or(p.CursorID(), "admin")), cascade)
		}
	case "pause":
		err = s.m.Pause(id)
	case "resume":
		err = s.m.ResumeCursor(id)
	case "message":
		from := cmp.Or(p.CursorID(), "admin")
		res, err = s.m.SendMessage(from, id, body.Body)
	case "transfer":
		if !p.Admin {
			if body.Workspace != nil && *body.Workspace != p.Cursor.Workspace {
				err = fmt.Errorf("%w: cursors may only transfer into their own workspace", core.ErrForbidden)
				break
			}
			if body.Parent != nil && (*body.Parent == "" || !s.m.IsAncestor(p.Cursor.ID, *body.Parent)) {
				err = fmt.Errorf("%w: new parent must be you or one of your descendants", core.ErrForbidden)
				break
			}
		}
		var tc *core.Cursor
		tc, err = s.m.Transfer(id, body.TransferReq)
		res = tc.Public()
	case "wait":
		var cs []*core.Cursor
		cs, err = s.m.WaitCursors(r.Context(), []string{id}, true, time.Duration(body.TimeoutMs)*time.Millisecond)
		if err != nil && len(cs) > 0 {
			res, err = map[string]any{"cursor": cs[0].Public(), "timeout": true}, nil
		} else if err == nil {
			res = map[string]any{"cursor": cs[0].Public(), "timeout": false}
		}
	default:
		err = fmt.Errorf("%w: unknown op %q", core.ErrNotFound, op)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) ownsWorkspace(p *Principal, ws string) bool {
	if p.Cursor == nil || !p.Cursor.Can("workspace") {
		return false
	}
	w, err := s.m.GetWorkspace(ws)
	return err == nil && w.Owner != "" && s.m.IsAncestor(p.Cursor.ID, w.Owner)
}

func need(p *Principal, perm string) error {
	if p.limited() && !p.Cursor.Can(perm) {
		return fmt.Errorf("%w: needs %s permission", core.ErrForbidden, perm)
	}
	return nil
}

func acquireAllowed(p *Principal, surface string) error {
	if !p.limited() {
		return nil
	}
	if surface == "workspace" {
		return fmt.Errorf("%w: cursors cannot lock the whole workspace; call human_request to hand over to a person", core.ErrForbidden)
	}
	if strings.HasPrefix(surface, "tab:") {
		return need(p, "browser")
	}
	return need(p, "desktop")
}

const maxCursorWorkspaces = 5

func (s *Server) createCursorWorkspace(ctx context.Context, c *core.Cursor, req core.CreateWorkspaceReq) (*core.Workspace, error) {
	if c.Workspace != "" {
		if parent, err := s.m.GetWorkspace(c.Workspace); err == nil {
			inheritLimits(&req.Config, parent.Config)
		}
	}
	start := req.Start
	req.Start = false
	s.wsMu.Lock()
	n := 0
	for _, v := range s.m.ListWorkspaces() {
		if v.State == core.StateDestroyed || v.Owner == "" {
			continue
		}
		if o, err := s.m.GetCursor(v.Owner); err == nil && o.Root == c.Root {
			n++
		}
	}
	if n >= maxCursorWorkspaces {
		s.wsMu.Unlock()
		return nil, fmt.Errorf("%w: cursor tree already has %d live workspaces (limit %d)", core.ErrForbidden, n, maxCursorWorkspaces)
	}
	ws, err := s.m.CreateWorkspace(ctx, req)
	s.wsMu.Unlock()
	if err != nil || !start {
		return ws, err
	}
	if err := s.m.Start(ctx, ws.ID); err != nil {
		return ws, err
	}
	return s.m.GetWorkspace(ws.ID)
}

func sandboxed(explicit *bool, admin, def bool) bool {
	if explicit != nil && (admin || !def) {
		return *explicit
	}
	return def
}

func inheritLimits(cfg *core.WorkspaceConfig, parent core.WorkspaceConfig) {
	if parent.Sandbox {
		cfg.Sandbox = true
	}
	limit := func(v *int, max int) {
		if max > 0 && (*v <= 0 || *v > max) {
			*v = max
		}
	}
	limit(&cfg.Budget.MemoryMB, parent.Budget.MemoryMB)
	limit(&cfg.Budget.CPUPercent, parent.Budget.CPUPercent)
	limit(&cfg.Budget.MaxCursors, parent.Budget.MaxCursors)
	limit(&cfg.Budget.MaxTabs, parent.Budget.MaxTabs)
}

func (s *Server) canKill(p *Principal, ws string, pid int) error {
	if !p.limited() {
		return nil
	}
	if err := need(p, "process"); err != nil {
		return err
	}
	if p.Cursor.Can("workspace") {
		return nil
	}
	owner, err := s.m.ProcessOwner(ws, pid)
	if err != nil {
		return err
	}
	if owner == "" || !s.m.IsAncestor(p.Cursor.ID, owner) {
		return fmt.Errorf("%w: process %d was not launched by you or your subagents", core.ErrForbidden, pid)
	}
	return nil
}

func (s *Server) canMessage(p *Principal, id string) bool {
	if p.Cursor == nil {
		return false
	}
	if s.m.IsAncestor(p.Cursor.ID, id) || s.m.IsAncestor(id, p.Cursor.ID) {
		return true
	}
	c, err := s.m.GetCursor(id)
	return err == nil && c.Workspace != "" && c.Workspace == p.Cursor.Workspace
}

func (s *Server) eventFilter(r *http.Request, p *Principal) (core.Filter, error) {
	q := r.URL.Query()
	f := core.Filter{Workspace: q.Get("workspace"), Cursor: q.Get("cursor")}
	if t := q.Get("types"); t != "" {
		f.Types = strings.Split(t, ",")
	}
	f.Since, _ = strconv.ParseInt(q.Get("since"), 10, 64)
	if !p.Admin {
		ws := ""
		if p.Cursor != nil {
			ws = p.Cursor.Workspace
		}
		if p.Viewer != nil {
			ws = p.Viewer.Workspace
		}
		if f.Workspace == "" {
			f.Workspace = ws
		}
		if f.Workspace != ws {
			return f, core.ErrForbidden
		}
		if ws == "" {
			if p.Cursor == nil {
				return f, core.ErrForbidden
			}
			f.Cursor = p.Cursor.ID
		}
	}
	return f, nil
}

func (s *Server) pollEvents(w http.ResponseWriter, r *http.Request, p *Principal) {
	f, err := s.eventFilter(r, p)
	if err != nil {
		writeErr(w, err)
		return
	}
	waitMs, _ := strconv.Atoi(r.URL.Query().Get("wait_ms"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if waitMs > 120000 {
		waitMs = 120000
	}
	var ch <-chan *core.Event
	if waitMs > 0 {
		var unsub func()
		ch, unsub = s.m.Bus.Subscribe(core.Filter{Workspace: f.Workspace, Cursor: f.Cursor, Types: f.Types}, 64)
		defer unsub()
	}
	evs := s.m.Bus.Recent(f, limit)
	if len(evs) == 0 && waitMs > 0 {
		select {
		case e := <-ch:
			evs = append(evs, e)
		case <-time.After(time.Duration(waitMs) * time.Millisecond):
		case <-r.Context().Done():
		}
	}
	writeJSON(w, 200, map[string]any{"events": evs, "seq": s.m.Bus.Seq()})
}

func (s *Server) sseEvents(w http.ResponseWriter, r *http.Request, p *Principal) {
	f, err := s.eventFilter(r, p)
	if err != nil {
		writeErr(w, err)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	fl.Flush()
	ch, unsub := s.m.Bus.Subscribe(f, 512)
	defer unsub()
	hb := time.NewTicker(25 * time.Second)
	defer hb.Stop()
	for {
		select {
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, b)
			fl.Flush()
		case <-hb.C:
			fmt.Fprint(w, ": hb\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
