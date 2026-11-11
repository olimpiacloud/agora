package core

import (
	"fmt"
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

func TestRemovedLogsAreBounded(t *testing.T) {
	b := NewBus(t.TempDir(), 16)
	for i := 0; i < maxGoneLogs+10; i++ {
		b.RemoveLog(fmt.Sprintf("ws%d", i))
	}
	b.RemoveLog("ws5000")
	b.RemoveLog("ws5000")
	if len(b.gone) != maxGoneLogs || len(b.goneQ) != maxGoneLogs {
		t.Fatalf("gone=%d queue=%d", len(b.gone), len(b.goneQ))
	}
	if b.gone["ws0"] || !b.gone["ws5000"] || !b.gone[fmt.Sprintf("ws%d", maxGoneLogs+9)] {
		t.Fatal("oldest removed workspace should be forgotten first")
	}
}
