package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/maphash"
	"image"
	"image/jpeg"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/olimpiacloud/agora/internal/core"
	"github.com/olimpiacloud/agora/internal/x11"
)

func (s *Server) streamJPEG(w http.ResponseWriter, r *http.Request, p *Principal) {
	ws := r.PathValue("id")
	control, err := s.viewerAccess(r, p, ws)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	fps, _ := strconv.Atoi(q.Get("fps"))
	if fps <= 0 || fps > 15 {
		fps = 5
	}
	maxW, _ := strconv.Atoi(q.Get("max_width"))
	if maxW <= 0 || maxW > 1920 {
		maxW = 1280
	}
	quality, _ := strconv.Atoi(q.Get("quality"))
	if quality <= 0 || quality > 95 {
		quality = 60
	}
	cursorID := q.Get("cursor")
	winID, _ := strconv.ParseUint(q.Get("window"), 0, 32)
	d, err := s.m.LiveDisplay(r.Context(), ws)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cursorID != "" {
		if _, err := s.m.CursorRegion(r.Context(), ws, cursorID); err != nil {
			writeErr(w, err)
			return
		}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := core.NewID("jpeg")
	send := func(b []byte) error {
		wctx, wc := context.WithTimeout(ctx, 5*time.Second)
		defer wc()
		return c.Write(wctx, websocket.MessageText, b)
	}
	v, err := s.m.AttachViewer(r.Context(), ws, id, "observation", control, nil, send)
	if err != nil {
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer s.m.DetachViewer(ws, id)
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				v.Input(data)
			}
		}
	}()
	cp, err := d.NewCapturer()
	if err != nil {
		return
	}
	defer cp.Close()
	dw, err := d.WatchDamage()
	if err != nil {
		return
	}
	defer dw.Close()
	gap := time.Second / time.Duration(fps)
	var frame *x11.Frame
	var last, resolved time.Time
	var buf bytes.Buffer
	var region, sent image.Rectangle
	var lastSum uint64
	seed := maphash.MakeSeed()
	cropped := cursorID != "" || winID != 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.Exited():
			return
		case <-dw.C:
		case <-time.After(10 * time.Second):
		}
		if wait := gap - time.Since(last); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		dw.Ack()
		if cropped && time.Since(resolved) > 1500*time.Millisecond {
			resolved = time.Now()
			if cursorID != "" {
				region, _ = s.m.CursorRegion(ctx, ws, cursorID)
			} else {
				region = core.WindowRegion(d, uint32(winID))
			}
			region = region.Intersect(image.Rect(0, 0, d.Width, d.Height))
		}
		if region.Dx() > 1 && region.Dy() > 1 {
			frame, err = cp.Grab(region.Min.X, region.Min.Y, region.Dx(), region.Dy(), frame)
		} else {
			frame, err = cp.GrabScreen(frame)
		}
		if err != nil {
			continue
		}
		if region != sent {
			msg, _ := json.Marshal(map[string]any{"type": "region", "x": region.Min.X, "y": region.Min.Y, "w": frame.Width, "h": frame.Height, "screen_w": d.Width, "screen_h": d.Height})
			if send(msg) != nil {
				return
			}
			sent = region
			lastSum = 0
		}
		sum := maphash.Bytes(seed, frame.Pix[:frame.Stride*frame.Height])
		if sum == lastSum {
			continue
		}
		lastSum = sum
		img := frame.RGBA()
		out := core.Downscale(img, maxW)
		buf.Reset()
		buf.Write(make([]byte, 8))
		if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: quality}); err != nil {
			continue
		}
		b := buf.Bytes()
		binary.BigEndian.PutUint16(b[0:], uint16(out.Bounds().Dx()))
		binary.BigEndian.PutUint16(b[2:], uint16(out.Bounds().Dy()))
		binary.BigEndian.PutUint16(b[4:], uint16(frame.Width))
		binary.BigEndian.PutUint16(b[6:], uint16(frame.Height))
		wctx, wc := context.WithTimeout(ctx, 10*time.Second)
		err = c.Write(wctx, websocket.MessageBinary, b)
		wc()
		if err != nil {
			return
		}
		last = time.Now()
	}
}
