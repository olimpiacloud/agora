package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func (m *Manager) agentCommand(c *Cursor, tok string) ([]string, []string, error) {
	l := c.Launch
	bin := m.cfg.CarbonBinary
	if bin == "" {
		bin, _ = os.Executable()
	}
	env := []string{
		"CARBON_URL=" + m.cfg.ListenURL,
		"CARBON_TOKEN=" + tok,
		"CARBON_CURSOR=" + c.ID,
		"CARBON_WORKSPACE=" + c.Workspace,
	}
	prompt := l.Prompt
	if prompt == "" {
		prompt = c.Goal
	}
	sys := fmt.Sprintf("You are Carbon cursor %s (%q) working in Carbon workspace %s. Use the carbon MCP tools to see and control the desktop and browser. Report progress with cursor_report and finish by calling cursor_complete with a concise structured result (or cursor_fail). If a CAPTCHA or login needs a human, call cursor_status with status needs_human and wait_events for control.returned.", c.ID, c.Name, c.Workspace)
	mcpCfg := map[string]any{"mcpServers": map[string]any{"carbon": map[string]any{"command": bin, "args": []string{"mcp"}, "env": map[string]string{"CARBON_URL": m.cfg.ListenURL, "CARBON_TOKEN": tok}}}}
	switch l.Adapter {
	case "claude", "claude-code":
		mb, _ := json.Marshal(mcpCfg)
		args := []string{"claude", "-p", prompt, "--mcp-config", string(mb), "--append-system-prompt", sys, "--allowedTools", "mcp__carbon__*", "--output-format", "text"}
		if l.Model != "" {
			args = append(args, "--model", l.Model)
		}
		return args, env, nil
	case "codex":
		args := []string{"codex", "exec", "--skip-git-repo-check",
			"-c", fmt.Sprintf("mcp_servers.carbon.command=%q", bin),
			"-c", `mcp_servers.carbon.args=["mcp"]`,
			"-c", fmt.Sprintf("mcp_servers.carbon.env={CARBON_URL=%q,CARBON_TOKEN=%q}", m.cfg.ListenURL, tok),
		}
		if l.Model != "" {
			args = append(args, "-m", l.Model)
		}
		args = append(args, sys+"\n\nTask: "+prompt)
		return args, env, nil
	case "command", "":
		if len(l.Command) == 0 {
			return nil, nil, fmt.Errorf("launch.command required")
		}
		return l.Command, append(env, "CARBON_GOAL="+prompt), nil
	}
	return nil, nil, fmt.Errorf("unknown adapter %q", l.Adapter)
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
	cmd.Env = append(os.Environ(), env...)
	for k, v := range c.Launch.Env {
		if !strings.HasPrefix(k, "CARBON_") {
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
			cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", wt.PortBase), fmt.Sprintf("CARBON_PORT_BASE=%d", wt.PortBase), fmt.Sprintf("CARBON_PORTS=%d", wt.Ports))
		}
		m.mu.Unlock()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logf, _ := os.OpenFile(filepath.Join(dir, "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	tb := &tailBuf{f: logf}
	cmd.Stdout = tb
	cmd.Stderr = tb
	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		return err
	}
	lp := &launchProc{cmd: cmd}
	m.mu.Lock()
	m.launches[id] = lp
	if cc := m.cursors[id]; cc != nil {
		cc.LaunchPid = cmd.Process.Pid
		cc.Status = CursorRunning
		m.saveCursor(cc)
	}
	m.mu.Unlock()
	m.Bus.Publish("agent.launched", c.Workspace, id, map[string]any{"adapter": c.Launch.Adapter, "pid": cmd.Process.Pid})
	go func() {
		err := cmd.Wait()
		if logf != nil {
			logf.Close()
		}
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
		out := strings.TrimSpace(tb.String())
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
