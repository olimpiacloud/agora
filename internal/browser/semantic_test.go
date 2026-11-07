package browser

import (
	"strings"
	"testing"
)

func TestCompactBoxes(t *testing.T) {
	s := &Snapshot{URL: "u", Elements: []Element{{Ref: "e1", Role: "link", Name: "A", Box: [4]int{1, 2, 3, 4}}}}
	if got := s.Compact(false); strings.Contains(got, "@") || !strings.Contains(got, "[e1] link \"A\"\n") {
		t.Fatalf("without boxes: %q", got)
	}
	if got := s.Compact(true); !strings.Contains(got, "[e1] link \"A\" @1,2 3x4\n") {
		t.Fatalf("with boxes: %q", got)
	}
}
