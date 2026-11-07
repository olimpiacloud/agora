package core

import (
	"testing"
	"time"
)

func TestUnsubscribeClosesChannel(t *testing.T) {
	b := NewBus(t.TempDir(), 16)
	ch, unsub := b.Subscribe(Filter{}, 4)
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	b.Publish("x", "ws", "", nil)
	unsub()
	unsub()
	b.Publish("y", "ws", "", nil)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscriber goroutine still running after unsubscribe")
	}
}
