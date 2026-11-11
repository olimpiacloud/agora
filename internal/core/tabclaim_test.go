package core

import (
	"errors"
	"sync"
	"testing"
)

func TestClaimTab(t *testing.T) {
	cases := []struct {
		name     string
		owner    string
		caller   string
		readOnly bool
		live     bool
		ancestor bool
		claim    bool
		err      bool
	}{
		{"admin never claims", "", "", false, false, false, false, false},
		{"admin passes an owned tab", "a", "", false, true, false, false, false},
		{"free tab mutating claims", "", "a", false, false, false, true, false},
		{"free tab read-only does not claim", "", "a", true, false, false, false, false},
		{"own tab", "a", "a", false, true, false, false, false},
		{"other live owner forbidden", "b", "a", false, true, false, false, true},
		{"other live owner forbidden read-only", "b", "a", true, true, false, false, true},
		{"ancestor of owner may act", "b", "a", false, true, true, false, false},
		{"ended owner frees the tab", "b", "a", false, false, false, true, false},
		{"ended owner read-only leaves it", "b", "a", true, false, false, false, false},
	}
	for _, tc := range cases {
		claim, err := claimTab(tc.owner, tc.caller, tc.readOnly, tc.live, tc.ancestor)
		if claim != tc.claim || (err != nil) != tc.err {
			t.Errorf("%s: claim=%v err=%v", tc.name, claim, err)
		}
		if err != nil && !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v is not ErrForbidden", tc.name, err)
		}
	}
}

func TestClaimTabForIsAtomic(t *testing.T) {
	m := &Manager{cursors: map[string]*Cursor{}}
	r := &Runtime{m: m, tabOwner: map[string]string{}}
	ids := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}
	for _, id := range ids {
		m.cursors[id] = &Cursor{ID: id, Status: CursorRunning}
	}
	for round := 0; round < 200; round++ {
		delete(r.tabOwner, "t")
		var wg sync.WaitGroup
		var mu sync.Mutex
		winners := 0
		for _, id := range ids {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				claim, err := m.claimTabFor(r, id, "t", false)
				if claim && err == nil {
					mu.Lock()
					winners++
					mu.Unlock()
				}
			}(id)
		}
		wg.Wait()
		if winners != 1 {
			t.Fatalf("round %d: %d cursors claimed the same tab", round, winners)
		}
	}
	r.tabOwner["t"] = "c1"
	if claim, err := m.claimTabFor(r, "c2", "t", true); claim || !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only access to another cursor's tab: claim=%v err=%v", claim, err)
	}
	delete(r.tabOwner, "t")
	if claim, err := m.claimTabFor(r, "c2", "t", true); claim || err != nil || r.tabOwner["t"] != "" {
		t.Fatalf("read-only access claimed the tab: claim=%v err=%v owner=%q", claim, err, r.tabOwner["t"])
	}
	m.cursors["c1"].Status = CursorDone
	r.tabOwner["t"] = "c1"
	if claim, err := m.claimTabFor(r, "c2", "t", false); !claim || err != nil || r.tabOwner["t"] != "c2" {
		t.Fatalf("ended owner did not free the tab: claim=%v err=%v owner=%q", claim, err, r.tabOwner["t"])
	}
}
