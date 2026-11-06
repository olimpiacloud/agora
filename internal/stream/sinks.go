package stream

import (
	"sync"
	"sync/atomic"
)

type QueueSink struct {
	id      string
	mode    atomic.Value
	ch      chan *Frame
	needKey atomic.Bool
	write   func(*Frame) error
	once    sync.Once
	done    chan struct{}
	Dropped atomic.Int64
	OnError func(error)
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

func (s *QueueSink) Push(f *Frame) bool {
	if s.needKey.Load() && !f.Key {
		s.Dropped.Add(1)
		return false
	}
	select {
	case s.ch <- f:
		s.needKey.Store(false)
		return true
	default:
		s.Dropped.Add(1)
		s.needKey.Store(true)
		return false
	}
}

func (s *QueueSink) run() {
	for {
		select {
		case <-s.done:
			return
		case f := <-s.ch:
			if err := s.write(f); err != nil {
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
