package core

import (
	"fmt"
	"sync"
	"time"
)

const (
	PrioAgent = 10
	PrioHuman = 100
)

type Lock struct {
	Surface  string    `json:"surface"`
	Holder   string    `json:"holder"`
	Priority int       `json:"priority"`
	Reason   string    `json:"reason,omitempty"`
	Since    time.Time `json:"since"`
	Expires  time.Time `json:"expires,omitempty"`
}

type ConflictError struct {
	Lock Lock
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("surface %s is controlled by %s (%s)", e.Lock.Surface, e.Lock.Holder, e.Lock.Reason)
}

type Arbiter struct {
	mu     sync.Mutex
	locks  map[string]map[string]*Lock
	onLost func(ws string, l Lock, by string)
}

func NewArbiter(onLost func(ws string, l Lock, by string)) *Arbiter {
	return &Arbiter{locks: map[string]map[string]*Lock{}, onLost: onLost}
}

func (a *Arbiter) get(ws, surface string) *Lock {
	m := a.locks[ws]
	if m == nil {
		return nil
	}
	l := m[surface]
	if l != nil && !l.Expires.IsZero() && time.Now().After(l.Expires) {
		delete(m, surface)
		return nil
	}
	return l
}

func (a *Arbiter) Acquire(ws, surface, holder string, prio int, ttl time.Duration, reason string) (*Lock, error) {
	a.mu.Lock()
	cur := a.get(ws, surface)
	if cur != nil && cur.Holder != holder && cur.Priority >= prio {
		c := *cur
		a.mu.Unlock()
		return nil, &ConflictError{Lock: c}
	}
	var lost *Lock
	if cur != nil && cur.Holder != holder {
		c := *cur
		lost = &c
	}
	l := &Lock{Surface: surface, Holder: holder, Priority: prio, Reason: reason, Since: time.Now()}
	if ttl > 0 {
		l.Expires = time.Now().Add(ttl)
	}
	if a.locks[ws] == nil {
		a.locks[ws] = map[string]*Lock{}
	}
	a.locks[ws][surface] = l
	out := *l
	a.mu.Unlock()
	if lost != nil && a.onLost != nil {
		a.onLost(ws, *lost, holder)
	}
	return &out, nil
}

func (a *Arbiter) Release(ws, surface, holder string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.get(ws, surface)
	if l == nil || (holder != "" && l.Holder != holder) {
		return false
	}
	delete(a.locks[ws], surface)
	return true
}

func (a *Arbiter) ReleaseHolder(holder string) []Lock {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Lock
	for _, m := range a.locks {
		for s, l := range m {
			if l.Holder == holder {
				out = append(out, *l)
				delete(m, s)
			}
		}
	}
	return out
}

func (a *Arbiter) Check(ws, surface, holder string, prio int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range []string{"workspace", surface} {
		if s == "" {
			continue
		}
		l := a.get(ws, s)
		if l != nil && l.Holder != holder && l.Priority >= prio {
			return &ConflictError{Lock: *l}
		}
	}
	return nil
}

func (a *Arbiter) List(ws string) []Lock {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []Lock{}
	for s := range a.locks[ws] {
		if l := a.get(ws, s); l != nil {
			out = append(out, *l)
		}
	}
	return out
}

func (a *Arbiter) Clear(ws string) {
	a.mu.Lock()
	delete(a.locks, ws)
	a.mu.Unlock()
}
