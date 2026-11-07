package stream

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestRTCSessionCloseOnce(t *testing.T) {
	s := &RTCSession{closed: make(chan struct{})}
	var calls atomic.Int32
	onClose := func() { calls.Add(1) }
	s.stateChanged(webrtc.PeerConnectionStateDisconnected, onClose)
	select {
	case <-s.Closed():
		t.Fatal("a transient disconnect must not end the session")
	default:
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := webrtc.PeerConnectionStateFailed
			if i%2 == 0 {
				st = webrtc.PeerConnectionStateClosed
			}
			s.stateChanged(st, onClose)
		}(i)
	}
	wg.Wait()
	<-s.Closed()
	if calls.Load() != 1 {
		t.Fatalf("OnClose called %d times", calls.Load())
	}
}
