package stream

import (
	"sync"
	"sync/atomic"
	"time"
)

const maxKeyBackoff = 8 * time.Second

var sinkNow = time.Now

type QueueSink struct {
	id      string
	mode    atomic.Value
	ch      chan *Frame
	needKey atomic.Bool
	dead    atomic.Bool
	write   func(*Frame) error
	once    sync.Once
	done    chan struct{}
	Dropped atomic.Int64
	OnError func(error)
	keyMu   sync.Mutex
	backoff time.Duration
	nextKey time.Time
	askedAt time.Time
	keyAt   time.Time
}

func NewQueueSink(id string, mode Mode, depth int, write func(*Frame) error) *QueueSink {
	s := &QueueSink{id: id, ch: make(chan *Frame, depth), write: write, done: make(chan struct{})}
	s.mode.Store(mode)
	s.needKey.Store(true)
	go s.run()
	return s
}

func (s *QueueSink) ID() string { return s.id }

func (s *QueueSink) Mode() Mode { return s.mode.Load().(Mode) }

func (s *QueueSink) SetMode(m Mode) { s.mode.Store(m) }

func (s *QueueSink) Dead() bool { return s.dead.Load() }

func (s *QueueSink) Push(f *Frame) bool {
	if s.dead.Load() {
		return true
	}
	if s.needKey.Load() && !f.Key {
		s.Dropped.Add(1)
		return !s.askKey(false)
	}
	select {
	case s.ch <- f:
		if f.Key {
			s.keyMu.Lock()
			s.keyAt = sinkNow()
			s.keyMu.Unlock()
		}
		s.needKey.Store(false)
		return true
	default:
		s.Dropped.Add(1)
		s.needKey.Store(true)
		return !s.askKey(true)
	}
}

func (s *QueueSink) askKey(overflow bool) bool {
	now := sinkNow()
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if overflow && s.keyAt.After(s.askedAt) && now.Sub(s.keyAt) > s.backoff {
		s.backoff = 0
	}
	if now.Before(s.nextKey) {
		return false
	}
	s.backoff = min(max(2*s.backoff, time.Second), maxKeyBackoff)
	s.askedAt = now
	s.nextKey = now.Add(s.backoff)
	return true
}

func (s *QueueSink) run() {
	for {
		select {
		case <-s.done:
			return
		case f := <-s.ch:
			if err := s.write(f); err != nil {
				s.dead.Store(true)
				if s.OnError != nil {
					s.OnError(err)
				}
				return
			}
		}
	}
}

func (s *QueueSink) Close() {
	s.once.Do(func() { close(s.done) })
}
