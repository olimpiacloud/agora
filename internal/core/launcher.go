package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type launchProc struct {
	cmd  *exec.Cmd
	once sync.Once
}

func (l *launchProc) kill() {
	l.once.Do(func() {
		if l.cmd.Process != nil {
			syscall.Kill(-l.cmd.Process.Pid, syscall.SIGTERM)
			go func(p int) {
				time.Sleep(5 * time.Second)
				syscall.Kill(-p, syscall.SIGKILL)
			}(l.cmd.Process.Pid)
		}
	})
}

type tailBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
	f   *os.File
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f != nil {
		t.f.Write(p)
	}
	t.buf.Write(p)
	if t.buf.Len() > 16384 {
		b := t.buf.Bytes()
		nb := make([]byte, 8192)
		copy(nb, b[len(b)-8192:])
		t.buf.Reset()
		t.buf.Write(nb)
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

const DefaultLeanModel = "sonnet"

func (m *Manager) agentCommand(c *Cursor, tok string) ([]string, []string, error) {
	l := c.Launch
	bin := m.cfg.AgoraBinary
	if bin == "" {
		bin, _ = os.Executable()
	}
	env := []string{
		"AGORA_URL=" + m.cfg.ListenURL,
		"AGORA_TOKEN=" + tok,
		"AGORA_CURSOR=" + c.ID,
		"AGORA_WORKSPACE=" + c.Workspace,
	}
	prompt := l.Prompt
	if prompt == "" {
		prompt = c.Goal
	}
	full := l.Profile == "full"
	groups := []string{"browser"}
	if len(l.Tools) > 0 {
		groups = append([]string{}, l.Tools...)
	}
	for _, perm := range []string{"desktop", "spawn", "workspace", "process"} {
		if c.Can(perm) {
			groups = append(groups, perm)
		}
	}
	mcpEnv := map[string]string{"AGORA_URL": m.cfg.ListenURL, "AGORA_TOKEN": tok}
	if !full {
		mcpEnv["AGORA_TOOLS"] = strings.Join(groups, ",")
		mcpEnv["AGORA_LEAN"] = "1"
	}
	sys := fmt.Sprintf("You are Agora cursor %s (%q) working in Agora workspace %s. Use the agora MCP tools to see and control the desktop and browser. Coordinate with sibling agents through the shared board: board_read before starting a work item, board_post kind=claim to take it (skip it if the claim fails), kind=done when finished. Report progress with cursor_report and finish by calling cursor_complete with a concise structured result (or cursor_fail). If a CAPTCHA or login needs a human, call human_request, then events_wait with types control.returned.", c.ID, c.Name, c.Workspace)
	lean := fmt.Sprintf(`You are an autonomous agent (Agora cursor %s, %q) working on a shared Linux desktop with Chromium. You act only through the agora tools. Be fast and economical:
- Open your own tab with browser_open and keep using its target.
- Action tools (navigate, click, fill, type, press, select) already return what is visible afterwards. Do not call browser_snapshot after them unless you need offscreen content.
- Prefer element refs over coordinates. Use browser_screenshot only when visual appearance matters.
- Put independent tool calls in the same turn.
- Sibling agents share this workspace. Before starting a work item, call board_read, then board_post kind=claim with a short topic; if the claim fails, skip that item. Post findings with kind=finding and finish items with kind=done.
- No destructive or irreversible actions unless the task asks. Never reveal secrets.
- If a CAPTCHA or login blocks you, call human_request, then events_wait with types control.returned.
- Finish with cursor_complete (structured result) or cursor_fail. Keep your final text to one line.`, c.ID, c.Name)
	switch l.Adapter {
	case "claude", "claude-code":
		dir := filepath.Join(m.cfg.Home, "agents", c.ID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, nil, err
		}
		cfgPath := filepath.Join(dir, "mcp.json")
		mb, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"agora": map[string]any{"command": bin, "args": []string{"mcp"}, "env": mcpEnv}}})
		if err := os.WriteFile(cfgPath, mb, 0600); err != nil {
			return nil, nil, err
		}
		var args []string
		if full {
			args = []string{"claude", "-p", positionalPrompt(prompt), "--mcp-config", cfgPath, "--append-system-prompt", sys, "--allowedTools", "mcp__agora__*", "--output-format", "json"}
		} else {
			args = []string{"claude", "-p", positionalPrompt(prompt), "--mcp-config", cfgPath, "--strict-mcp-config", "--disable-slash-commands", "--tools", "", "--setting-sources", "", "--system-prompt", lean, "--allowedTools", "mcp__agora__*", "--output-format", "json"}
		}
		model := l.Model
		if model == "" && !full {
			model = DefaultLeanModel
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		if l.Effort != "" {
			args = append(args, "--effort", l.Effort)
		}
		return args, env, nil
	case "codex":
		names := []string{}
		for k, v := range mcpEnv {
			names = append(names, fmt.Sprintf("%q", k))
			if k != "AGORA_URL" && k != "AGORA_TOKEN" {
				env = append(env, k+"="+v)
			}
		}
		sort.Strings(names)
		args := []string{"codex", "exec", "--skip-git-repo-check", "--json",
			"-c", fmt.Sprintf("mcp_servers.agora.command=%q", bin),
			"-c", `mcp_servers.agora.args=["mcp"]`,
			"-c", "mcp_servers.agora.env_vars=[" + strings.Join(names, ",") + "]",
		}
		if l.Model != "" {
			args = append(args, "-m", l.Model)
		}
		if l.Effort != "" {
			args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", l.Effort))
		}
		instr := sys
		if !full {
			instr = lean
		}
		args = append(args, positionalPrompt(instr+"\n\nTask: "+prompt))
		return args, env, nil
	case "command", "":
		if len(l.Command) == 0 {
			return nil, nil, fmt.Errorf("launch.command required")
		}
		return l.Command, append(env, "AGORA_GOAL="+prompt), nil
	}
	return nil, nil, fmt.Errorf("unknown adapter %q", l.Adapter)
}

func positionalPrompt(p string) string {
	if strings.HasPrefix(p, "-") {
		return "Task: " + p
	}
	return p
}

var agentEnvKeys = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "LANG": true, "TZ": true, "TMPDIR": true, "TERM": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_STATE_HOME": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true,
}

var agentEnvPrefixes = []string{"LC_", "ANTHROPIC_", "CLAUDE_", "OPENAI_", "CODEX_", "AWS_", "GOOGLE_", "CLOUD_ML_", "AZURE_", "VERTEX_"}

func agentEnv(environ []string) ([]string, []string) {
	out, dropped := []string{}, []string{}
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if agentEnvAllowed(k) {
			out = append(out, kv)
		} else {
			dropped = append(dropped, k)
		}
	}
	return out, dropped
}

func agentEnvAllowed(k string) bool {
	if agentEnvKeys[k] {
		return true
	}
	for _, p := range agentEnvPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func (m *Manager) launchAgent(id, tok string) error {
	c, err := m.GetCursor(id)
	if err != nil {
		return err
	}
	args, env, err := m.agentCommand(c, tok)
	if err != nil {
		return err
	}
	cmd := exec.Command(args[0], args[1:]...)
	kept, dropped := agentEnv(os.Environ())
	if len(dropped) > 0 {
		log.Printf("cursor %s: not passing environment variables outside the agent allowlist: %s", id, strings.Join(dropped, ", "))
	}
	cmd.Env = append(kept, env...)
	for k, v := range c.Launch.Env {
		if !strings.HasPrefix(k, "AGORA_") {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	dir := filepath.Join(m.cfg.Home, "agents", id)
	os.MkdirAll(dir, 0700)
	cmd.Dir = dir
	if c.Worktree != "" {
		m.mu.Lock()
		if wt := m.worktrees[c.Worktree]; wt != nil {
			cmd.Dir = wt.Path
			cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", wt.PortBase), fmt.Sprintf("AGORA_PORT_BASE=%d", wt.PortBase), fmt.Sprintf("AGORA_PORTS=%d", wt.Ports))
		}
		m.mu.Unlock()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logf, _ := os.OpenFile(filepath.Join(dir, "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	tb := &tailBuf{f: logf}
	stdout := &tailBuf{f: logf}
	cmd.Stdout = stdout
	cmd.Stderr = tb
	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		os.Remove(filepath.Join(dir, "mcp.json"))
		return err
	}
	lp := &launchProc{cmd: cmd}
	m.mu.Lock()
	m.launches[id] = lp
	if cc := m.cursors[id]; cc != nil {
		cc.LaunchPid = cmd.Process.Pid
		m.saveCursor(cc)
	}
	m.mu.Unlock()
	if m.setCursorStatus(id, CursorRunning, "", false) != nil {
		lp.kill()
	} else {
		m.Bus.Publish("agent.launched", c.Workspace, id, map[string]any{"adapter": c.Launch.Adapter, "pid": cmd.Process.Pid})
	}
	go func() {
		err := cmd.Wait()
		if logf != nil {
			logf.Close()
		}
		os.Remove(filepath.Join(dir, "mcp.json"))
		m.mu.Lock()
		delete(m.launches, id)
		m.mu.Unlock()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		out := strings.TrimSpace(stdout.String())
		u, text := parseClaudeResult(out)
		if u == nil {
			u, text = parseCodexResult(out)
		}
		if u != nil {
			out = text
			m.mu.Lock()
			if cc := m.cursors[id]; cc != nil {
				cc.Usage = u
				m.saveCursor(cc)
			}
			m.mu.Unlock()
			m.Bus.Publish("agent.usage", c.Workspace, id, map[string]any{"cost_usd": u.CostUSD, "turns": u.Turns, "input": u.InputTokens, "output": u.OutputTokens, "cache_read": u.CacheReadTokens, "cache_write": u.CacheWriteTokens})
		}
		if out == "" {
			out = strings.TrimSpace(tb.String())
		}
		if len(out) > 4000 {
			out = out[len(out)-4000:]
		}
		m.Bus.Publish("agent.exited", c.Workspace, id, map[string]any{"code": code})
		cur, err := m.GetCursor(id)
		if err != nil || cur.Status.Terminal() {
			return
		}
		if code == 0 {
			res, _ := json.Marshal(map[string]any{"output": out, "reported": false})
			m.Complete(id, res)
		} else {
			m.Fail(id, fmt.Sprintf("agent exited with code %d: %s", code, lastLines(out, 8)))
		}
	}()
	return nil
}

func lastLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}

func (m *Manager) killLaunch(id string) {
	m.mu.Lock()
	l := m.launches[id]
	m.mu.Unlock()
	if l != nil {
		l.kill()
	}
}

func (m *Manager) signalLaunch(id string, stop bool) {
	m.mu.Lock()
	l := m.launches[id]
	m.mu.Unlock()
	if l == nil || l.cmd.Process == nil {
		return
	}
	sig := syscall.SIGCONT
	if stop {
		sig = syscall.SIGSTOP
	}
	syscall.Kill(-l.cmd.Process.Pid, sig)
}

func parseClaudeResult(out string) (*Usage, string) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "{") || !strings.Contains(l, `"type":"result"`) {
			continue
		}
		if u, t, ok := decodeClaudeResult(l); ok {
			return u, t
		}
	}
	return nil, ""
}

func parseCodexResult(out string) (*Usage, string) {
	var u Usage
	text := ""
	found := false
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "{") {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Usage *struct {
				In     int64 `json:"input_tokens"`
				Cached int64 `json:"cached_input_tokens"`
				Out    int64 `json:"output_tokens"`
			} `json:"usage"`
			Item *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(l), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "turn.completed":
			if ev.Usage != nil {
				found = true
				u.Turns++
				u.InputTokens += ev.Usage.In - ev.Usage.Cached
				u.CacheReadTokens += ev.Usage.Cached
				u.OutputTokens += ev.Usage.Out
			}
		case "item.completed":
			if ev.Item != nil && (ev.Item.Type == "agent_message" || ev.Item.Type == "assistant_message") && ev.Item.Text != "" {
				text = ev.Item.Text
			}
		}
	}
	if !found {
		return nil, ""
	}
	return &u, text
}

func decodeClaudeResult(l string) (*Usage, string, bool) {
	var r struct {
		Result     string  `json:"result"`
		NumTurns   int     `json:"num_turns"`
		Cost       float64 `json:"total_cost_usd"`
		DurationMs int64   `json:"duration_ms"`
		Usage      struct {
			In  int64 `json:"input_tokens"`
			Out int64 `json:"output_tokens"`
			CR  int64 `json:"cache_read_input_tokens"`
			CW  int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(l), &r) != nil {
		return nil, "", false
	}
	return &Usage{InputTokens: r.Usage.In, OutputTokens: r.Usage.Out, CacheReadTokens: r.Usage.CR, CacheWriteTokens: r.Usage.CW, CostUSD: r.Cost, Turns: r.NumTurns, DurationMs: r.DurationMs}, r.Result, true
}
