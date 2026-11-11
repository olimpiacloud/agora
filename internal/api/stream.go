package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/olimpiacloud/agora/internal/core"
	"github.com/olimpiacloud/agora/internal/stream"
)

func (s *Server) viewerAccess(r *http.Request, p *Principal, ws string) (bool, error) {
	if p.Admin {
		return true, nil
	}
	if p.Viewer != nil {
		if p.Viewer.Workspace != ws {
			return false, core.ErrForbidden
		}
		return p.Viewer.Control, nil
	}
	if p.Cursor != nil && p.Cursor.Workspace == ws {
		return false, nil
	}
	return false, core.ErrForbidden
}

func modeOf(r *http.Request) stream.Mode {
	if r.URL.Query().Get("mode") == "observation" {
		return stream.Observation
	}
	return stream.Interactive
}

func (s *Server) streamWS(w http.ResponseWriter, r *http.Request, p *Principal) {
	ws := r.PathValue("id")
	control, err := s.viewerAccess(r, p, ws)
	if err != nil {
		writeErr(w, err)
		return
	}
	if s.byeIfAsleep(w, r, ws) {
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := core.NewID("view")
	send := func(b []byte) error {
		wctx, wc := context.WithTimeout(ctx, 5*time.Second)
		defer wc()
		return c.Write(wctx, websocket.MessageText, b)
	}
	sink := stream.NewQueueSink(id, modeOf(r), 4, func(f *stream.Frame) error {
		buf := make([]byte, 13+len(f.Data))
		if f.Key {
			buf[0] = 1
		}
		binary.BigEndian.PutUint64(buf[1:], uint64(f.PTS))
		binary.BigEndian.PutUint16(buf[9:], uint16(f.Width))
		binary.BigEndian.PutUint16(buf[11:], uint16(f.Height))
		copy(buf[13:], f.Data)
		wctx, wc := context.WithTimeout(ctx, 10*time.Second)
		defer wc()
		return c.Write(wctx, websocket.MessageBinary, buf)
	})
	sink.OnError = func(error) { cancel() }
	v, err := s.m.AttachViewer(r.Context(), ws, id, modeOf(r), control, sink, send)
	if err != nil {
		sink.Close()
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer s.m.DetachViewer(ws, id)
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			v.Input(data)
		}
	}
}

func (s *Server) byeIfAsleep(w http.ResponseWriter, r *http.Request, ws string) bool {
	if r.URL.Query().Get("wake") != "0" || !s.m.Asleep(ws) {
		return false
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return true
	}
	b, _ := json.Marshal(map[string]any{"type": "bye", "reason": "suspended"})
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	c.Write(ctx, websocket.MessageText, b)
	c.Close(websocket.StatusNormalClosure, "suspended")
	return true
}

func (s *Server) streamRTC(w http.ResponseWriter, r *http.Request, p *Principal) {
	ws := r.PathValue("id")
	control, err := s.viewerAccess(r, p, ws)
	if err != nil {
		writeErr(w, err)
		return
	}
	if r.URL.Query().Get("wake") == "0" && s.m.Asleep(ws) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "workspace suspended", "suspended": true})
		return
	}
	var req struct {
		SDP  string `json:"sdp"`
		Mode string `json:"mode"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	mode := stream.Interactive
	if req.Mode == "observation" {
		mode = stream.Observation
	}
	id := core.NewID("rtc")
	var viewer *core.Viewer
	var eng func()
	sess, answer, err := s.m.RTC().Answer(r.Context(), id, mode, req.SDP, stream.RTCHandlers{
		OnInput: func(b []byte) {
			if viewer != nil {
				viewer.Input(b)
			}
		},
		OnKeyframe: func() {
			if eng != nil {
				eng()
			}
		},
		OnClose: func() {
			go s.m.DetachViewer(ws, id)
		},
		OnEventsDC: func(send func([]byte) error) {
			if viewer != nil {
				viewer.SetSend(send)
			}
		},
	})
	if err != nil {
		writeErr(w, fmt.Errorf("webrtc: %w", err))
		return
	}
	v, err := s.m.AttachViewer(context.Background(), ws, id, mode, control, sess.Sink, nil)
	if err != nil {
		sess.Close()
		writeErr(w, err)
		return
	}
	viewer = v
	eng = func() { v.Input([]byte(`{"t":"key_frame"}`)) }
	go func() {
		<-sess.Closed()
		sess.Close()
	}()
	writeJSON(w, 200, map[string]any{"sdp": answer, "viewer": id})
}
