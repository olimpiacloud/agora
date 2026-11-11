package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/client"
)

type slowAPI struct {
	started   chan struct{}
	cancelled chan struct{}
}

func newSlowServer(t *testing.T) (*Server, *slowAPI, *bytes.Buffer) {
	api := &slowAPI{started: make(chan struct{}, 4), cancelled: make(chan struct{}, 4)}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/me" {
			json.NewEncoder(w).Encode(map[string]any{"admin": true})
			return
		}
		io.Copy(io.Discard, r.Body)
		api.started <- struct{}{}
		select {
		case <-r.Context().Done():
			api.cancelled <- struct{}{}
		case <-time.After(10 * time.Second):
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	}))
	t.Cleanup(hs.Close)
	s := New(&client.Client{URL: hs.URL, HTTP: hs.Client()})
	var out bytes.Buffer
	s.out = &out
	return s, api, &out
}

func (s *Server) output() string {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	return s.out.(*bytes.Buffer).String()
}

func waitFor(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

const slowCall = `{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"browser_eval","arguments":{"workspace":"w","target":"T","expression":"1"}}}` + "\n"

func send(t *testing.T, w io.Writer, format string, args ...any) {
	t.Helper()
	line := format
	if len(args) > 0 {
		line = strings.Replace(format, "%s", args[0].(string), 1)
	}
	if _, err := io.WriteString(w, line); err != nil {
		t.Fatal(err)
	}
}

func TestToolCallCancelledByNotification(t *testing.T) {
	s, api, _ := newSlowServer(t)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Serve(t.Context(), pr) }()
	send(t, pw, slowCall, "7")
	waitFor(t, api.started, "the tool call to reach the API")
	send(t, pw, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7,"reason":"user"}}`+"\n")
	waitFor(t, api.cancelled, "the API request to be cancelled")
	send(t, pw, `{"jsonrpc":"2.0","id":8,"method":"ping"}`+"\n")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(s.output(), `"id":8`) {
		if time.Now().After(deadline) {
			t.Fatalf("no ping response: %q", s.output())
		}
		time.Sleep(10 * time.Millisecond)
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.output(), `"id":7`) {
		t.Fatalf("a cancelled request was answered: %q", s.output())
	}
}

func TestShutdownCancelsInflightCalls(t *testing.T) {
	s, api, _ := newSlowServer(t)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Serve(t.Context(), pr) }()
	send(t, pw, slowCall, `"a"`)
	waitFor(t, api.started, "the tool call to reach the API")
	pw.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve kept waiting for an in-flight call after stdin closed")
	}
	waitFor(t, api.cancelled, "the API request to be cancelled")
}
