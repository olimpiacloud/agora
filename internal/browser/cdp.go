package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type Event struct {
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params"`
}

type cdpMsg struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *CDPError       `json:"error,omitempty"`
}

type CDPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *CDPError) Error() string { return fmt.Sprintf("cdp %d: %s %s", e.Code, e.Message, e.Data) }

type CDP struct {
	w       io.Writer
	wmu     sync.Mutex
	next    atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpMsg
	subs    map[int]func(Event)
	subID   int
	closed  chan struct{}
	err     error
}

var ErrClosed = errors.New("browser connection closed")

func NewCDP(r io.Reader, w io.Writer) *CDP {
	c := &CDP{w: w, pending: map[int64]chan cdpMsg{}, subs: map[int]func(Event){}, closed: make(chan struct{})}
	go c.read(r)
	return c
}

func (c *CDP) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		b, err := br.ReadBytes(0)
		if err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			close(c.closed)
			return
		}
		b = b[:len(b)-1]
		var m cdpMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		ev := Event{Method: m.Method, SessionID: m.SessionID, Params: m.Params}
		c.mu.Lock()
		fns := make([]func(Event), 0, len(c.subs))
		for _, f := range c.subs {
			fns = append(fns, f)
		}
		c.mu.Unlock()
		for _, f := range fns {
			f(ev)
		}
	}
}

func (c *CDP) Subscribe(f func(Event)) func() {
	c.mu.Lock()
	c.subID++
	id := c.subID
	c.subs[id] = f
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

func (c *CDP) Done() <-chan struct{} { return c.closed }

func (c *CDP) Call(ctx context.Context, session, method string, params any, out any) error {
	id := c.next.Add(1)
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	b, err := json.Marshal(cdpMsg{ID: id, Method: method, SessionID: session, Params: raw})
	if err != nil {
		return err
	}
	ch := make(chan cdpMsg, 1)
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return ErrClosed
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()
	c.wmu.Lock()
	_, err = c.w.Write(append(b, 0))
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}
