package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/olimpiacloud/agora/internal/stream"
)

func waitStatus(t *testing.T, m *Manager, id string, want CursorStatus, within time.Duration) *Cursor {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		c, err := m.GetCursor(id)
		if err != nil {
			t.Fatal(err)
		}
		if c.Status == want {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("cursor %s is %s, want %s", id, c.Status, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func launchGone(m *Manager, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.launches[id] == nil
}

func newRuntimeLimitedManager(t *testing.T, limit time.Duration) *Manager {
	t.Helper()
	t.Setenv("AGORA_CGROUPS", "off")
	m, err := NewManager(Config{Home: t.TempDir(), RTC: stream.RTCOptions{IncludeLoop: true}, AgentMaxRuntime: limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	return m
}

func TestLaunchMaxRuntimeFailsAndKillsTheAgent(t *testing.T) {
	m := newTestManager(t)
	c, _, err := m.CreateCursor(context.Background(), CreateCursorReq{Name: "a", Launch: &LaunchSpec{Adapter: "command", Command: []string{"sleep", "30"}, MaxRuntime: Duration{Duration: 300 * time.Millisecond}}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitStatus(t, m, c.ID, CursorFailed, 3*time.Second)
	if !strings.Contains(got.Error, "maximum runtime") {
		t.Fatalf("error: %q", got.Error)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !launchGone(m, c.ID) {
		if time.Now().After(deadline) {
			t.Fatal("agent process still tracked 3 seconds after the runtime limit")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDaemonMaxRuntimeAppliesWhenTheLaunchSetsNone(t *testing.T) {
	m := newRuntimeLimitedManager(t, 300*time.Millisecond)
	c, _, err := m.CreateCursor(context.Background(), CreateCursorReq{Name: "a", Launch: &LaunchSpec{Adapter: "command", Command: []string{"sleep", "30"}}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitStatus(t, m, c.ID, CursorFailed, 3*time.Second)
	if !strings.Contains(got.Error, "maximum runtime") {
		t.Fatalf("error: %q", got.Error)
	}
}

func TestLaunchMaxRuntimeLeavesAFinishedAgentAlone(t *testing.T) {
	m := newTestManager(t)
	c, _, err := m.CreateCursor(context.Background(), CreateCursorReq{Name: "a", Launch: &LaunchSpec{Adapter: "command", Command: []string{"true"}, MaxRuntime: Duration{Duration: 300 * time.Millisecond}}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, c.ID, CursorDone, 3*time.Second)
	time.Sleep(600 * time.Millisecond)
	got, _ := m.GetCursor(c.ID)
	if got.Status != CursorDone || got.Error != "" {
		t.Fatalf("finished cursor became %s with error %q", got.Status, got.Error)
	}
}

func TestNoMaxRuntimeMeansNoLimit(t *testing.T) {
	m := newTestManager(t)
	c, _, err := m.CreateCursor(context.Background(), CreateCursorReq{Name: "a", Launch: &LaunchSpec{Adapter: "command", Command: []string{"sleep", "30"}}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	got, _ := m.GetCursor(c.ID)
	if got.Status.Terminal() {
		t.Fatalf("cursor without a runtime limit ended as %s: %q", got.Status, got.Error)
	}
}

func TestNegativeMaxRuntimeIsRefused(t *testing.T) {
	m := newTestManager(t)
	c, _, err := m.CreateCursor(context.Background(), CreateCursorReq{Name: "a", Launch: &LaunchSpec{Adapter: "command", Command: []string{"sleep", "30"}, MaxRuntime: Duration{Duration: -time.Second}}})
	if c != nil || err == nil || !strings.Contains(err.Error(), "max_runtime") {
		t.Fatalf("negative max_runtime: cursor %v, err %v", c, err)
	}
}
