package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeChrome struct {
	t      *testing.T
	in     *io.PipeReader
	out    *io.PipeWriter
	wmu    sync.Mutex
	mu     sync.Mutex
	calls  []cdpMsg
	handle func(m cdpMsg) (any, *CDPError)
}

func newFake(t *testing.T, handle func(m cdpMsg) (any, *CDPError)) (*fakeChrome, *CDP) {
	toUs, fromChrome := io.Pipe()
	toChrome, fromUs := io.Pipe()
	f := &fakeChrome{t: t, in: toChrome, out: fromChrome, handle: handle}
	c := NewCDP(toUs, fromUs)
	t.Cleanup(func() {
		fromChrome.Close()
		toChrome.Close()
		fromUs.Close()
	})
	return f, c
}

func (f *fakeChrome) serve() {
	go func() {
		br := bufio.NewReader(f.in)
		for {
			b, err := br.ReadBytes(0)
			if err != nil {
				return
			}
			var m cdpMsg
			if json.Unmarshal(b[:len(b)-1], &m) != nil {
				continue
			}
			f.mu.Lock()
			f.calls = append(f.calls, m)
			f.mu.Unlock()
			go func() {
				res, cerr := f.handle(m)
				if res == nil && cerr == nil {
					return
				}
				r := cdpMsg{ID: m.ID, Error: cerr}
				if res != nil {
					r.Result, _ = json.Marshal(res)
				}
				f.send(r)
			}()
		}
	}()
}

func (f *fakeChrome) send(m cdpMsg) {
	b, _ := json.Marshal(m)
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.out.Write(append(b, 0))
}

func (f *fakeChrome) methods() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, c := range f.calls {
		out[c.Method]++
	}
	return out
}

func (f *fakeChrome) params(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.calls {
		if c.Method == method {
			var p map[string]any
			json.Unmarshal(c.Params, &p)
			out = append(out, p)
		}
	}
	return out
}

func within(t *testing.T, d time.Duration, f func() error) error {
	t.Helper()
	ch := make(chan error, 1)
	go func() { ch <- f() }()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		t.Fatal("call did not return in time")
		return nil
	}
}

func TestCDPCallAfterClose(t *testing.T) {
	f, c := newFake(t, func(m cdpMsg) (any, *CDPError) { return nil, nil })
	f.serve()
	inflight := make(chan error, 1)
	go func() { inflight <- c.Call(context.Background(), "", "Browser.getVersion", nil, nil) }()
	for len(f.methods()) == 0 {
		time.Sleep(time.Millisecond)
	}
	f.out.Close()
	select {
	case err := <-inflight:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("in-flight call: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight call hung after close")
	}
	<-c.Done()
	err := within(t, 2*time.Second, func() error { return c.Call(context.Background(), "", "Browser.getVersion", nil, nil) })
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
}

func TestCDPWriteHonorsContext(t *testing.T) {
	_, c := newFake(t, nil)
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		err := within(t, 2*time.Second, func() error { return c.Call(ctx, "", "Browser.getVersion", nil, nil) })
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call %d with a stuck writer: %v", i, err)
		}
	}
}

func TestCDPReply(t *testing.T) {
	f, c := newFake(t, func(m cdpMsg) (any, *CDPError) {
		if m.Method == "Bad" {
			return nil, &CDPError{Code: -32000, Message: "nope"}
		}
		return map[string]any{"product": "Fake/1"}, nil
	})
	f.serve()
	var v struct {
		Product string `json:"product"`
	}
	if err := c.Call(context.Background(), "", "Browser.getVersion", nil, &v); err != nil || v.Product != "Fake/1" {
		t.Fatalf("reply %v %q", err, v.Product)
	}
	var ce *CDPError
	if err := c.Call(context.Background(), "", "Bad", nil, nil); !errors.As(err, &ce) {
		t.Fatalf("error reply: %v", err)
	}
}

func attachHandler(enable func() *CDPError) func(m cdpMsg) (any, *CDPError) {
	return func(m cdpMsg) (any, *CDPError) {
		switch m.Method {
		case "Target.createBrowserContext":
			return map[string]any{"browserContextId": "ctx1"}, nil
		case "Target.createTarget":
			return map[string]any{"targetId": "T1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "S1"}, nil
		case "Page.enable":
			time.Sleep(50 * time.Millisecond)
			if e := enable(); e != nil {
				return nil, e
			}
		}
		return map[string]any{}, nil
	}
}

func TestAttachSingleFlight(t *testing.T) {
	f, c := newFake(t, attachHandler(func() *CDPError { return nil }))
	f.serve()
	b := newBrowser(LaunchOptions{}, c)
	c.Subscribe(b.onEvent)
	var wg sync.WaitGroup
	tabs := make([]*Tab, 5)
	for i := range tabs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tab, err := b.Attach(context.Background(), "T1")
			if err != nil {
				t.Error(err)
			}
			tabs[i] = tab
		}(i)
	}
	wg.Wait()
	for _, tab := range tabs {
		if tab == nil || tab != tabs[0] {
			t.Fatal("concurrent attaches returned different tabs")
		}
	}
	if n := f.methods()["Target.attachToTarget"]; n != 1 {
		t.Fatalf("attachToTarget called %d times", n)
	}
	if n := f.methods()["Runtime.enable"]; n != 0 {
		t.Fatalf("Runtime.enable called %d times", n)
	}
	if b.Tab("T1") != tabs[0] {
		t.Fatal("tab not published")
	}
}

func TestAttachFailureCleanup(t *testing.T) {
	f, c := newFake(t, attachHandler(func() *CDPError { return &CDPError{Code: -32000, Message: "enable failed"} }))
	f.serve()
	b := newBrowser(LaunchOptions{}, c)
	c.Subscribe(b.onEvent)
	if _, err := b.Attach(context.Background(), "T1"); err == nil {
		t.Fatal("attach should fail")
	}
	if b.Tab("T1") != nil || len(b.sessions) != 0 || len(b.attaching) != 0 {
		t.Fatal("half-attached tab left behind")
	}
	if d := f.params("Target.detachFromTarget"); len(d) != 1 || d[0]["sessionId"] != "S1" {
		t.Fatalf("detach: %v", d)
	}
	if _, err := b.NewTab(context.Background(), NewTabOptions{URL: "https://example.com", Isolated: true}); err == nil {
		t.Fatal("new tab should fail")
	}
	if p := f.params("Target.closeTarget"); len(p) != 1 || p[0]["targetId"] != "T1" {
		t.Fatalf("close target: %v", p)
	}
	if p := f.params("Target.disposeBrowserContext"); len(p) != 1 || p[0]["browserContextId"] != "ctx1" {
		t.Fatalf("dispose context: %v", p)
	}
	if len(b.contexts) != 0 {
		t.Fatalf("contexts left: %v", b.contexts)
	}
}

func TestNewTabSetsContextBeforePublish(t *testing.T) {
	f, c := newFake(t, attachHandler(func() *CDPError { return nil }))
	f.serve()
	b := newBrowser(LaunchOptions{}, c)
	tab, err := b.NewTab(context.Background(), NewTabOptions{URL: "https://example.com", Isolated: true})
	if err != nil {
		t.Fatal(err)
	}
	if tab.ContextID != "ctx1" || !tab.ownsContext || b.Tab("T1") != tab {
		t.Fatalf("tab %+v", tab)
	}
	if _, err := b.NewTab(context.Background(), NewTabOptions{URL: "file:///etc/passwd"}); err == nil {
		t.Fatal("file URL accepted")
	}
	if n := f.methods()["Target.createTarget"]; n != 1 {
		t.Fatalf("createTarget called %d times", n)
	}
}

func TestAllowedURL(t *testing.T) {
	for u, ok := range map[string]bool{
		"":                            true,
		"about:blank":                 true,
		"https://example.com/a?b#c":   true,
		"http://127.0.0.1:8080/":      true,
		"HTTPS://Example.com":         true,
		"data:text/html,<b>x</b>":     true,
		"file:///etc/passwd":          false,
		"chrome://settings":           false,
		"view-source:https://a.com":   false,
		"devtools://devtools/x.html":  false,
		"chrome-extension://abc/x":    false,
		"javascript:alert(1)":         false,
		"JavaScript:alert(1)":         false,
		" javascript:alert(1)":        false,
		"about:config":                false,
		"example.com":                 false,
		"http:///nohost":              false,
		"blob:https://a.com/uuid":     false,
		"filesystem:https://a.com/x":  false,
		"chrome-untrusted://terminal": false,
	} {
		if err := AllowedURL(u); (err == nil) != ok {
			t.Errorf("AllowedURL(%q) = %v, want ok=%v", u, err, ok)
		}
	}
}
