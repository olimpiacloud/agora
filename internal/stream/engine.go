package stream

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/olimpiacloud/agora/internal/media"
	"github.com/olimpiacloud/agora/internal/x11"
)

type Mode string

const (
	Interactive Mode = "interactive"
	Observation Mode = "observation"
)

type Frame struct {
	Data     []byte
	Key      bool
	PTS      int64
	Duration time.Duration
	Width    int
	Height   int
}

type Sink interface {
	ID() string
	Mode() Mode
	Push(f *Frame) bool
	Close()
}

type Stats struct {
	Running   bool    `json:"running"`
	Sinks     int     `json:"sinks"`
	Frames    int64   `json:"frames"`
	Bytes     int64   `json:"bytes"`
	Keyframes int64   `json:"keyframes"`
	EncodeMs  float64 `json:"encode_ms_avg"`
	CaptureMs float64 `json:"capture_ms_avg"`
	FPS       float64 `json:"fps"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Mode      Mode    `json:"mode"`
	Starts    int64   `json:"starts"`
	Encoder   string  `json:"encoder,omitempty"`
	LatencyMs float64 `json:"latency_ms_avg"`
	Fallbacks int64   `json:"encoder_fallbacks,omitempty"`
}

type Engine struct {
	d        *x11.Display
	mu       sync.Mutex
	sinks    map[string]Sink
	running  bool
	stop     chan struct{}
	done     chan struct{}
	keyReq   atomic.Bool
	wake     chan struct{}
	frames   atomic.Int64
	bytes    atomic.Int64
	keys     atomic.Int64
	starts   atomic.Int64
	encNs    atomic.Int64
	capNs    atomic.Int64
	latNs    atomic.Int64
	latN     atomic.Int64
	fallback atomic.Int64
	encName  atomic.Value
	lastFPS  atomic.Int64
	OnState  func(running bool)
	interact atomic.Int64
	mode     Mode
	w, h     int
}

func NewEngine(d *x11.Display) *Engine {
	return &Engine{d: d, sinks: map[string]Sink{}, wake: make(chan struct{}, 1)}
}

func (e *Engine) poke() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) AddSink(s Sink) {
	e.mu.Lock()
	e.sinks[s.ID()] = s
	start := !e.running
	if start {
		e.running = true
		e.stop = make(chan struct{})
		e.done = make(chan struct{})
		go e.loop(e.stop, e.done)
	}
	e.mu.Unlock()
	e.keyReq.Store(true)
	e.poke()
	if start && e.OnState != nil {
		e.OnState(true)
	}
}

func (e *Engine) RemoveSink(id string) {
	e.mu.Lock()
	s := e.sinks[id]
	delete(e.sinks, id)
	var stop, done chan struct{}
	if len(e.sinks) == 0 && e.running {
		e.running = false
		stop, done = e.stop, e.done
	}
	e.mu.Unlock()
	if s != nil {
		s.Close()
	}
	if stop != nil {
		close(stop)
		<-done
		if e.OnState != nil {
			e.OnState(false)
		}
	}
}

func (e *Engine) Close() {
	e.mu.Lock()
	ids := make([]string, 0, len(e.sinks))
	for id := range e.sinks {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.RemoveSink(id)
	}
}

func (e *Engine) RequestKeyframe() {
	e.keyReq.Store(true)
	e.poke()
}

func (e *Engine) Damage() { e.poke() }

func (e *Engine) Interact() { e.interact.Store(time.Now().UnixNano()) }

func (e *Engine) recentInput() bool {
	return time.Since(time.Unix(0, e.interact.Load())) < 2*time.Second
}

func (e *Engine) Stats() Stats {
	e.mu.Lock()
	n := len(e.sinks)
	r := e.running
	m := e.mode
	w, h := e.w, e.h
	e.mu.Unlock()
	f := e.frames.Load()
	s := Stats{Running: r, Sinks: n, Frames: f, Bytes: e.bytes.Load(), Keyframes: e.keys.Load(), Mode: m, Width: w, Height: h, Starts: e.starts.Load(), FPS: float64(e.lastFPS.Load()) / 10, Fallbacks: e.fallback.Load()}
	s.Encoder, _ = e.encName.Load().(string)
	if f > 0 {
		s.EncodeMs = float64(e.encNs.Load()) / float64(f) / 1e6
		s.CaptureMs = float64(e.capNs.Load()) / float64(f) / 1e6
	}
	if ln := e.latN.Load(); ln > 0 {
		s.LatencyMs = float64(e.latNs.Load()) / float64(ln) / 1e6
	}
	return s
}

func (e *Engine) snapshotSinks() ([]Sink, Mode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Sink, 0, len(e.sinks))
	mode := Observation
	for _, s := range e.sinks {
		out = append(out, s)
		if s.Mode() == Interactive {
			mode = Interactive
		}
	}
	e.mode = mode
	return out, mode
}

func params(m Mode, active bool) (fps, crf, kbps int) {
	if m == Interactive {
		if active {
			return 30, 23, 8000
		}
		return 15, 23, 8000
	}
	return 8, 28, 2000
}

func (e *Engine) loop(stop, done chan struct{}) {
	defer close(done)
	e.starts.Add(1)
	cp, err := e.d.NewCapturer()
	if err != nil {
		return
	}
	defer cp.Close()
	dw, err := e.d.WatchDamage()
	if err != nil {
		return
	}
	defer dw.Close()
	var enc media.Encoder
	defer func() {
		if enc != nil {
			enc.Close()
		}
	}()
	var frame *x11.Frame
	var curMode Mode
	needFull := true
	start := time.Now()
	last := time.Time{}
	fpsWin := time.Now()
	fpsCount := 0
	var glass time.Time
	failures := 0
	for {
		select {
		case <-stop:
			return
		case <-dw.C:
			if glass.IsZero() {
				glass = time.Now()
			}
		case <-e.wake:
		}
		sinks, mode := e.snapshotSinks()
		if len(sinks) == 0 {
			continue
		}
		fps, crf, kbps := params(mode, e.recentInput())
		minGap := time.Second / time.Duration(fps)
		if wait := minGap - time.Since(last); wait > 0 {
			select {
			case <-stop:
				return
			case <-time.After(wait):
			}
		}
		w, h := e.d.Width, e.d.Height
		if enc == nil || w&^1 != e.w || h&^1 != e.h {
			if enc != nil {
				enc.Close()
			}
			enc, err = media.NewEncoder(media.H264Options{Width: w, Height: h, FPS: fps, CRF: crf, MaxKbps: kbps})
			if err != nil {
				return
			}
			e.encName.Store(enc.Name())
			e.mu.Lock()
			e.w, e.h = w&^1, h&^1
			e.mu.Unlock()
			curMode = mode
			e.keyReq.Store(true)
			needFull = true
		} else if mode != curMode {
			enc.Reconfigure(crf, kbps)
			curMode = mode
		}
		t0 := time.Now()
		key := e.keyReq.Swap(false)
		now := time.Now()
		pts := now.Sub(start).Milliseconds()
		var data []byte
		var isKey bool
		rx, ry, rw, rh, ok := dw.AckRegion()
		ew, eh := e.w, e.h
		var t1 time.Time
		if needFull || !ok {
			frame, err = cp.Grab(0, 0, ew, eh, frame)
			if err != nil {
				continue
			}
			t1 = time.Now()
			data, isKey, err = enc.Encode(frame.Pix, frame.Stride, frame.Width, frame.Height, pts, key || needFull)
			needFull = false
		} else {
			x0, y0 := rx&^1, ry&^1
			x1, y1 := (rx+rw+1)&^1, (ry+rh+1)&^1
			if x1 > ew {
				x1 = ew
			}
			if y1 > eh {
				y1 = eh
			}
			if x1-x0 < 2 || y1-y0 < 2 {
				if !key {
					glass = time.Time{}
					continue
				}
				x1, y1 = x0, y0
			}
			if x1 > x0 && y1 > y0 {
				frame, err = cp.Grab(x0, y0, x1-x0, y1-y0, frame)
				if err != nil {
					continue
				}
			}
			t1 = time.Now()
			if frame != nil && x1 > x0 && y1 > y0 {
				data, isKey, err = enc.EncodeRect(frame.Pix, frame.Stride, x0, y0, x1-x0, y1-y0, pts, key)
			} else {
				data, isKey, err = enc.EncodeRect(nil, 0, 0, 0, 0, 0, pts, key)
			}
		}
		t2 := time.Now()
		if err != nil {
			media.EncoderFailed(enc, err)
			enc.Close()
			enc = nil
			e.fallback.Add(1)
			if failures++; failures > 5 {
				return
			}
			needFull = true
			e.keyReq.Store(true)
			e.poke()
			continue
		}
		failures = 0
		if len(data) == 0 {
			continue
		}
		if !glass.IsZero() {
			e.latNs.Add(int64(t2.Sub(glass)))
			e.latN.Add(1)
			glass = time.Time{}
		}
		dur := now.Sub(last)
		if last.IsZero() || dur > time.Second {
			dur = minGap
		}
		last = now
		e.frames.Add(1)
		e.bytes.Add(int64(len(data)))
		e.capNs.Add(int64(t1.Sub(t0)))
		e.encNs.Add(int64(t2.Sub(t1)))
		if isKey {
			e.keys.Add(1)
		}
		fpsCount++
		if el := time.Since(fpsWin); el >= time.Second {
			e.lastFPS.Store(int64(float64(fpsCount) / el.Seconds() * 10))
			fpsCount = 0
			fpsWin = time.Now()
		}
		f := &Frame{Data: data, Key: isKey, PTS: pts, Duration: dur, Width: e.w, Height: e.h}
		for _, s := range sinks {
			if !s.Push(f) {
				e.keyReq.Store(true)
			}
		}
	}
}
