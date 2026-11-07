package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type BoardPost struct {
	Seq    int64     `json:"seq"`
	Time   time.Time `json:"time"`
	Cursor string    `json:"cursor"`
	Kind   string    `json:"kind"`
	Topic  string    `json:"topic,omitempty"`
	Text   string    `json:"text,omitempty"`
}

type BoardClaim struct {
	Topic  string    `json:"topic"`
	Cursor string    `json:"cursor"`
	Name   string    `json:"name,omitempty"`
	Status string    `json:"status"`
	Since  time.Time `json:"since"`
}

type ClaimConflict struct {
	Claim BoardClaim
}

func (e *ClaimConflict) Error() string {
	if e.Claim.Status == "done" {
		return fmt.Sprintf("topic %q was already finished by %s (%s)", e.Claim.Topic, e.Claim.Cursor, e.Claim.Name)
	}
	return fmt.Sprintf("topic %q is claimed by %s (%s)", e.Claim.Topic, e.Claim.Cursor, e.Claim.Name)
}

type board struct {
	posts  []BoardPost
	claims map[string]*BoardClaim
}

type BoardView struct {
	Seq    int64        `json:"seq"`
	Posts  []BoardPost  `json:"posts"`
	Claims []BoardClaim `json:"claims"`
}

var boardKinds = map[string]bool{"note": true, "finding": true, "claim": true, "release": true, "done": true}

func normTopic(t string) string {
	return strings.Join(strings.Fields(strings.ToLower(t)), " ")
}

func (m *Manager) BoardPost(ws, cursor, kind, topic, text string) (*BoardPost, error) {
	if kind == "" {
		kind = "note"
	}
	if !boardKinds[kind] {
		return nil, fmt.Errorf("kind must be note, finding, claim, release or done")
	}
	topic = normTopic(topic)
	if len(topic) > 200 || len(text) > 4000 {
		return nil, fmt.Errorf("topic is limited to 200 bytes and text to 4000")
	}
	if topic == "" && kind != "note" && kind != "finding" {
		return nil, fmt.Errorf("%s needs a topic", kind)
	}
	if text == "" && topic == "" {
		return nil, fmt.Errorf("empty post")
	}
	holder := defStr(cursor, "admin")
	m.mu.Lock()
	if w := m.ws[ws]; w == nil || w.State == StateDestroyed {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	name := holder
	if c := m.cursors[cursor]; c != nil {
		name = c.Name
	}
	if m.boards == nil {
		m.boards = map[string]*board{}
	}
	b := m.boards[ws]
	if b == nil {
		b = &board{claims: map[string]*BoardClaim{}}
		m.boards[ws] = b
	}
	now := time.Now().UTC()
	cur := b.claims[topic]
	if cur != nil && m.staleClaim(cur) {
		delete(b.claims, topic)
		cur = nil
	}
	switch kind {
	case "claim":
		if cur != nil && cur.Cursor != holder {
			cp := *cur
			m.mu.Unlock()
			return nil, &ClaimConflict{Claim: cp}
		}
		b.claims[topic] = &BoardClaim{Topic: topic, Cursor: holder, Name: name, Status: "claimed", Since: now}
	case "release":
		if cur == nil || cur.Status != "claimed" || (cur.Cursor != holder && cursor != "") {
			m.mu.Unlock()
			return nil, fmt.Errorf("you do not hold a claim on %q", topic)
		}
		delete(b.claims, topic)
	case "done":
		if cur != nil && cur.Cursor != holder && cur.Status == "claimed" {
			cp := *cur
			m.mu.Unlock()
			return nil, &ClaimConflict{Claim: cp}
		}
		b.claims[topic] = &BoardClaim{Topic: topic, Cursor: holder, Name: name, Status: "done", Since: now}
	}
	m.mailSeq++
	p := BoardPost{Seq: m.mailSeq, Time: now, Cursor: holder, Kind: kind, Topic: topic, Text: text}
	b.posts = append(b.posts, p)
	if len(b.posts) > 500 {
		b.posts = b.posts[len(b.posts)-500:]
	}
	m.mu.Unlock()
	m.Bus.Publish("board.post", ws, cursor, map[string]any{"seq": p.Seq, "kind": kind, "topic": topic})
	return &p, nil
}

func (m *Manager) BoardRead(ws string, since int64, limit int) *BoardView {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v := &BoardView{Seq: m.mailSeq, Posts: []BoardPost{}, Claims: []BoardClaim{}}
	b := m.boards[ws]
	if b == nil {
		return v
	}
	for _, p := range b.posts {
		if p.Seq > since {
			v.Posts = append(v.Posts, p)
		}
	}
	if len(v.Posts) > limit {
		v.Posts = v.Posts[len(v.Posts)-limit:]
	}
	for t, c := range b.claims {
		if m.staleClaim(c) {
			delete(b.claims, t)
			continue
		}
		v.Claims = append(v.Claims, *c)
	}
	sort.Slice(v.Claims, func(i, j int) bool { return v.Claims[i].Since.Before(v.Claims[j].Since) })
	return v
}

func (m *Manager) staleClaim(c *BoardClaim) bool {
	if c.Status != "claimed" || c.Cursor == "admin" {
		return false
	}
	cc := m.cursors[c.Cursor]
	return cc == nil || cc.Status.Terminal()
}

func (m *Manager) dropBoard(ws string) {
	m.mu.Lock()
	delete(m.boards, ws)
	m.mu.Unlock()
}
