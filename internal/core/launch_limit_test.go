package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/olimpiacloud/agora/internal/stream"
)

func newLimitedManager(t *testing.T, max int) *Manager {
	t.Helper()
	t.Setenv("AGORA_CGROUPS", "off")
	m, err := NewManager(Config{Home: t.TempDir(), RTC: stream.RTCOptions{IncludeLoop: true}, MaxAgents: max})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	return m
}

func sleepLaunch() *LaunchSpec {
	return &LaunchSpec{Adapter: "command", Command: []string{"sleep", "30"}}
}

func TestLaunchedAgentLimit(t *testing.T) {
	m := newLimitedManager(t, 2)
	ctx := context.Background()
	first, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "a", Launch: sleepLaunch()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "b", Launch: sleepLaunch()}); err != nil {
		t.Fatal(err)
	}
	c, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "c", Launch: sleepLaunch()})
	if c != nil || err == nil || !strings.Contains(err.Error(), "launched agent limit reached") {
		t.Fatalf("third launch: cursor %v, err %v", c, err)
	}
	if _, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "plain"}); err != nil {
		t.Fatalf("cursor without launch: %v", err)
	}
	if err := m.CancelCursor(first.ID, "test", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "d", Launch: sleepLaunch()}); err != nil {
		t.Fatalf("launch after a slot freed: %v", err)
	}
}

func TestLaunchedAgentLimitHoldsUnderConcurrency(t *testing.T) {
	m := newLimitedManager(t, 3)
	const n = 12
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = m.CreateCursor(context.Background(), CreateCursorReq{Name: "c", Launch: sleepLaunch()})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 3 {
		t.Fatalf("%d launches succeeded, want 3: %v", ok, errs)
	}
}

func TestLaunchedAgentLimitZeroIsUnlimited(t *testing.T) {
	m := newLimitedManager(t, 0)
	for i := 0; i < 5; i++ {
		if _, _, err := m.CreateCursor(context.Background(), CreateCursorReq{Name: "c", Launch: sleepLaunch()}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChildBudgetCannotExceedParent(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	parent, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "p", Budget: Budget{MaxCursors: 2}})
	if err != nil {
		t.Fatal(err)
	}
	big, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "big", Parent: parent.ID, Budget: Budget{MaxCursors: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if big.Budget.MaxCursors != 2 {
		t.Fatalf("child asking for 50 got max_cursors %d", big.Budget.MaxCursors)
	}
	unset, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "unset", Parent: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if unset.Budget.MaxCursors != 2 {
		t.Fatalf("child without a budget got max_cursors %d", unset.Budget.MaxCursors)
	}
	free, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "free"})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := m.CreateCursor(ctx, CreateCursorReq{Name: "child", Parent: free.ID, Budget: Budget{MaxCursors: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if child.Budget.MaxCursors != 50 {
		t.Fatalf("child of an unbudgeted parent got max_cursors %d", child.Budget.MaxCursors)
	}
}
