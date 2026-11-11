package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"image"
	"image/jpeg"
	"net/http"
	"strconv"
	"sync"
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
	if s.byeIfAsleep(w, r, ws) {
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
	key := jpegKey{d: d, cursor: cursorID, win: winID, fps: fps, maxW: maxW, quality: quality}
	sub, err := jpegSubscribe(ws, key, func(ctx context.Context, pub func(bool, []byte)) {
		s.produceJPEG(ctx, ws, d, cursorID, winID, fps, maxW, quality, pub)
	})
	if err != nil {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": err.Error()})
		return
	}
	defer sub.close()
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer c.CloseNow()
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
	var regionSeq, frameSeq int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.notify:
		}
		region, rs, frame, fs, dead := sub.p.snapshot()
		if dead {
			return
		}
		if rs != regionSeq && region != nil {
			if send(region) != nil {
				return
			}
			regionSeq = rs
		}
		if fs != frameSeq && frame != nil {
			wctx, wc := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageBinary, frame)
			wc()
			if err != nil {
				return
			}
			frameSeq = fs
		}
	}
}

const maxJPEGViewers = 32

type jpegKey struct {
	d                  *x11.Display
	cursor             string
	win                uint64
	fps, maxW, quality int
}

type jpegProducer struct {
	mu        sync.Mutex
	subs      map[*jpegSub]struct{}
	region    []byte
	frame     []byte
	regionSeq int64
	frameSeq  int64
	dead      bool
	cancel    context.CancelFunc
}

type jpegSub struct {
	ws     string
	key    jpegKey
	p      *jpegProducer
	notify chan struct{}
	once   sync.Once
}

var jpegHub = struct {
	sync.Mutex
	prods map[jpegKey]*jpegProducer
	views map[string]int
}{prods: map[jpegKey]*jpegProducer{}, views: map[string]int{}}

func (p *jpegProducer) snapshot() ([]byte, int64, []byte, int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.region, p.regionSeq, p.frame, p.frameSeq, p.dead
}

func (p *jpegProducer) wake() {
	for s := range p.subs {
		select {
		case s.notify <- struct{}{}:
		default:
		}
	}
}

func (p *jpegProducer) publish(text bool, b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if text {
		p.region = b
		p.regionSeq++
	} else {
		p.frame = b
		p.frameSeq++
	}
	p.wake()
}

func jpegSubscribe(ws string, key jpegKey, run func(context.Context, func(bool, []byte))) (*jpegSub, error) {
	jpegHub.Lock()
	defer jpegHub.Unlock()
	if jpegHub.views[ws] >= maxJPEGViewers {
		return nil, fmt.Errorf("too many JPEG viewers on this workspace (max %d)", maxJPEGViewers)
	}
	jpegHub.views[ws]++
	p := jpegHub.prods[key]
	if p == nil {
		ctx, cancel := context.WithCancel(context.Background())
		p = &jpegProducer{subs: map[*jpegSub]struct{}{}, cancel: cancel}
		jpegHub.prods[key] = p
		go func() {
			run(ctx, p.publish)
			jpegHub.Lock()
			if jpegHub.prods[key] == p {
				delete(jpegHub.prods, key)
			}
			jpegHub.Unlock()
			p.mu.Lock()
			p.dead = true
			p.wake()
			p.mu.Unlock()
			cancel()
		}()
	}
	sub := &jpegSub{ws: ws, key: key, p: p, notify: make(chan struct{}, 1)}
	p.mu.Lock()
	p.subs[sub] = struct{}{}
	sub.notify <- struct{}{}
	p.mu.Unlock()
	return sub, nil
}

func (s *jpegSub) close() {
	s.once.Do(func() {
		jpegHub.Lock()
		defer jpegHub.Unlock()
		if jpegHub.views[s.ws]--; jpegHub.views[s.ws] <= 0 {
			delete(jpegHub.views, s.ws)
		}
		s.p.mu.Lock()
		delete(s.p.subs, s)
		empty := len(s.p.subs) == 0
		s.p.mu.Unlock()
		if empty {
			if jpegHub.prods[s.key] == s.p {
				delete(jpegHub.prods, s.key)
			}
			s.p.cancel()
		}
	})
}

func (s *Server) produceJPEG(ctx context.Context, ws string, d *x11.Display, cursorID string, winID uint64, fps, maxW, quality int, pub func(bool, []byte)) {
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
			pub(true, msg)
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
		b := bytes.Clone(buf.Bytes())
		binary.BigEndian.PutUint16(b[0:], uint16(out.Bounds().Dx()))
		binary.BigEndian.PutUint16(b[2:], uint16(out.Bounds().Dy()))
		binary.BigEndian.PutUint16(b[4:], uint16(frame.Width))
		binary.BigEndian.PutUint16(b[6:], uint16(frame.Height))
		pub(false, b)
		last = time.Now()
	}
}
