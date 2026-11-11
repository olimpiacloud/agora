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

func TestRTCSessionAsksForKeyframeWhenConnected(t *testing.T) {
	q := NewQueueSink("rtc", Interactive, 4, func(*Frame) error { return nil })
	defer q.Close()
	var asks atomic.Int32
	q.SetKeyRequest(func() { asks.Add(1) })
	q.Push(&Frame{Key: true})
	s := &RTCSession{Sink: q, closed: make(chan struct{})}
	s.stateChanged(webrtc.PeerConnectionStateConnected, nil)
	if asks.Load() != 1 {
		t.Fatalf("%d keyframe requests on connect", asks.Load())
	}
	if !q.needKey.Load() {
		t.Fatal("deltas written before the track was bound must not be followed by more deltas")
	}
	select {
	case <-s.Closed():
		t.Fatal("connected closed the session")
	default:
	}
}
