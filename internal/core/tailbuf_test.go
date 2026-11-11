package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTailBufKeepsLongResultLine(t *testing.T) {
	text := strings.Repeat("x", 20<<10)
	line, _ := json.Marshal(map[string]any{"type": "result", "result": text, "num_turns": 3, "total_cost_usd": 0.5, "usage": map[string]any{"input_tokens": 10, "output_tokens": 20}})
	tb := &tailBuf{}
	tb.Write([]byte(`{"type":"system"}` + "\n"))
	for i := 0; i < len(line); i += 1000 {
		tb.Write(line[i:min(i+1000, len(line))])
	}
	tb.Write([]byte("\n\n"))
	if s := tb.String(); len(s) > 16384 {
		t.Fatalf("byte tail grew to %d", len(s))
	}
	out := tb.Output()
	u, got := parseClaudeResult(out)
	if u == nil || got != text || u.Turns != 3 || u.OutputTokens != 20 {
		t.Fatalf("usage %+v, result %d bytes", u, len(got))
	}

	tb = &tailBuf{}
	tb.Write([]byte("line one\nline two"))
	if out := tb.Output(); out != "line one\nline two" {
		t.Fatalf("short output %q", out)
	}

	tb = &tailBuf{}
	tb.Write([]byte(`{"type":"turn.started"}` + "\n"))
	tb.Write([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":"` + text + `"}}` + "\n"))
	tb.Write([]byte(`{"type":"turn.completed","usage":{"input_tokens":5,"output_tokens":7}}`))
	if u, _ := parseCodexResult(tb.Output()); u == nil || u.OutputTokens != 7 || u.Turns != 1 {
		t.Fatalf("codex usage %+v", u)
	}

	tb = &tailBuf{}
	big := make([]byte, maxResultLine+10)
	for i := range big {
		big[i] = 'y'
	}
	tb.Write(big)
	tb.Write([]byte("\n"))
	if out := tb.Output(); len(out) > 16384 {
		t.Fatalf("oversized line kept: %d bytes", len(out))
	}
}
