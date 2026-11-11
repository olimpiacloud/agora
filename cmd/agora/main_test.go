package main

import "testing"

func TestParseOpenAcceptsFlagsAfterWorkspace(t *testing.T) {
	cases := []struct {
		args    []string
		ws      string
		control bool
		base    string
	}{
		{[]string{"ws_1"}, "ws_1", true, ""},
		{[]string{"ws_1", "--control=false", "--base", "https://a.example"}, "ws_1", false, "https://a.example"},
		{[]string{"--control=false", "ws_1", "--base=https://b.example"}, "ws_1", false, "https://b.example"},
	}
	for _, c := range cases {
		o, err := parseOpen(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if o.ws != c.ws || o.control != c.control || o.base != c.base {
			t.Fatalf("%v: got %+v", c.args, o)
		}
	}
	for _, bad := range [][]string{{}, {"ws_1", "extra"}, {"ws_1", "--nope"}} {
		if _, err := parseOpen(bad); err == nil {
			t.Fatalf("%v: want an error", bad)
		}
	}
}
