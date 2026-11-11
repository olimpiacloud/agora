package browser

import (
	"encoding/json"
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

func TestCompactARIAState(t *testing.T) {
	yes, no := true, false
	s := &Snapshot{URL: "u", Elements: []Element{
		{Ref: "e1", Role: "tab", Name: "A", Selected: true},
		{Ref: "e2", Role: "button", Name: "Menu", Expanded: &yes},
		{Ref: "e3", Role: "button", Name: "More", Expanded: &no, Disabled: true},
		{Ref: "e4", Role: "switch", Name: "Dark", Checked: true},
		{Ref: "e5", Role: "link", Name: "Plain"},
	}}
	got := s.Compact(false)
	for _, want := range []string{
		"[e1] tab \"A\" selected\n",
		"[e2] button \"Menu\" expanded\n",
		"[e3] button \"More\" collapsed disabled\n",
		"[e4] switch \"Dark\" checked\n",
		"[e5] link \"Plain\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	var e Element
	if err := json.Unmarshal([]byte(`{"ref":"e1","role":"button","name":"x","box":[0,0,1,1],"expanded":false,"selected":true}`), &e); err != nil || e.Expanded == nil || *e.Expanded || !e.Selected {
		t.Fatalf("decode %+v %v", e, err)
	}
}
