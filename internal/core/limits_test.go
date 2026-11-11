package core

import (
	"fmt"
	"strings"
	"testing"
)

func TestClip(t *testing.T) {
	long := strings.Repeat("é", 10)
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"ééé", 2, "éé"},
		{"abc", 0, ""},
		{long, 20, long},
	}
	for _, c := range cases {
		if got := clip(c.in, c.n); got != c.want {
			t.Errorf("clip(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestCheckLabels(t *testing.T) {
	labels := func(n int) map[string]string {
		l := map[string]string{}
		for i := 0; i < n; i++ {
			l[fmt.Sprintf("k%d", i)] = "v"
		}
		return l
	}
	cases := []struct {
		name string
		in   map[string]string
		ok   bool
	}{
		{"nil", nil, true},
		{"32 entries", labels(32), true},
		{"33 entries", labels(33), false},
		{"long key", map[string]string{strings.Repeat("k", 65): "v"}, false},
		{"long value", map[string]string{"k": strings.Repeat("v", 257)}, false},
	}
	for _, c := range cases {
		if err := checkLabels(c.in); (err == nil) != c.ok {
			t.Errorf("%s: checkLabels error = %v, want ok %v", c.name, err, c.ok)
		}
	}
}
