package core

import (
	"context"
	"os"
	"runtime"
	"sort"
	"syscall"
	"time"
)

type WorkspaceUsage struct {
	Time     time.Time          `json:"time"`
	PSSMB    float64            `json:"pss_mb"`
	RSSMB    float64            `json:"rss_mb"`
	CPU      float64            `json:"cpu_percent"`
	Procs    int                `json:"procs"`
	ByRole   map[string]float64 `json:"pss_by_role_mb"`
	Top      []ProcUsage        `json:"top"`
	CgroupMB float64            `json:"cgroup_mb,omitempty"`
	Frozen   bool               `json:"frozen,omitempty"`
	Detailed bool               `json:"detailed"`
}

type cpuSample struct {
	t     time.Time
	ticks map[int]uint64
	usec  uint64
}

func (m *Manager) schedule() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
		}
		n++
		m.lifecycleTick()
		if n%30 == 0 {
			m.reapCursors()
		}
		if n%5 == 0 {
			m.usageTick()
		}
	}
}

func (m *Manager) lifecycleTick() {
	now := time.Now()
	m.mu.Lock()
	type item struct {
		r *Runtime
		w Workspace
	}
	var items []item
	var expired []string
	for id, w := range m.ws {
		if w.Config.TTL.Duration > 0 && w.State != StateDestroyed && now.Sub(w.Created) > w.Config.TTL.Duration {
			expired = append(expired, id)
			continue
		}
		if r := m.rt[id]; r != nil {
			items = append(items, item{r, *w})
		}
	}
	m.mu.Unlock()
	for _, id := range expired {
		m.Bus.Publish("workspace.expired", id, "", nil)
		go m.Destroy(context.Background(), id, false)
	}
	for _, it := range items {
		r := it.r
		r.mu.Lock()
		idleFor := now.Sub(r.activity)
		viewers := len(r.viewers)
		frozen := r.frozen
		idle := r.idle
		r.mu.Unlock()
		suspendAfter := it.w.Config.SuspendAfter.Duration
		if suspendAfter == 0 {
			suspendAfter = m.cfg.SuspendAfter
		}
		if frozen {
			if suspendAfter > 0 && idleFor > suspendAfter {
				m.suspendIdle(it.w.ID, r, suspendAfter, "idle")
			}
			continue
		}
		r.releaseSeats(m.cfg.SeatIdle)
		m.reap(it.w.ID, r)
		if viewers > 0 {
			if suspendAfter > 0 && idleFor > suspendAfter {
				m.suspendIdle(it.w.ID, r, suspendAfter, "idle")
			}
			continue
		}
		if m.hasActiveAgents(it.w.ID) && idleFor < 30*time.Minute {
			continue
		}
		if idleAfter := it.w.Config.IdleAfter.Duration; !idle && idleAfter > 0 && idleFor > idleAfter {
			if m.markIdle(it.w.ID, r, idleAfter) && it.w.Config.FreezeOnIdle {
				go m.freezeIfIdle(it.w.ID, r, idleAfter)
			}
			continue
		}
		if suspendAfter > 0 && idleFor > suspendAfter {
			m.suspendIdle(it.w.ID, r, suspendAfter, "idle")
		}
	}
}

func (m *Manager) markIdle(id string, r *Runtime, threshold time.Duration) bool {
	r.mu.Lock()
	flip := !r.idle && !r.closing && time.Since(r.activity) > threshold
	if flip {
		r.idle = true
	}
	r.mu.Unlock()
	if !flip {
		return false
	}
	r.releaseSeats(0)
	m.setState(id, StateIdle, "no activity")
	r.mu.Lock()
	still := r.idle
	r.mu.Unlock()
	if !still {
		m.setState(id, StateActive, "activity")
	}
	return still
}

func (m *Manager) suspendIdle(id string, r *Runtime, threshold time.Duration, reason string) {
	if !r.claimSuspend() {
		return
	}
	go m.suspendIfIdle(context.Background(), id, r, threshold, reason)
}

func (m *Manager) reap(id string, r *Runtime) {
	if m.cfg.TabIdle <= 0 {
		return
	}
	r.mu.Lock()
	if r.reaping || r.closing {
		r.mu.Unlock()
		return
	}
	r.reaping = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.reaping = false
			r.mu.Unlock()
		}()
		m.reapApps(id, r)
		r.mu.Lock()
		human := r.human
		r.mu.Unlock()
		if time.Since(human) > m.cfg.TabIdle {
			m.reapTabs(id, r)
		}
	}()
}

func (m *Manager) reapApps(id string, r *Runtime) {
	type app struct {
		pid     int
		owner   string
		started time.Time
	}
	var apps []app
	r.mu.Lock()
	for pid, p := range r.procs {
		if p.App.Owner != "" {
			apps = append(apps, app{pid, p.App.Owner, p.Started})
		}
	}
	r.mu.Unlock()
	for _, a := range apps {
		if time.Since(a.started) < m.cfg.TabIdle || m.ownerActive(a.owner) {
			continue
		}
		syscall.Kill(-a.pid, syscall.SIGTERM)
		m.Bus.Publish("scheduler.app_stopped", id, a.owner, map[string]any{"pid": a.pid, "reason": "owner ended"})
	}
}

func (m *Manager) reapTabs(id string, r *Runtime) {
	r.brMu.Lock()
	b := r.browser
	r.brMu.Unlock()
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tabs := b.Tabs()
	open := len(tabs)
	for _, t := range tabs {
		if time.Since(t.LastUsed()) < m.cfg.TabIdle {
			continue
		}
		r.mu.Lock()
		owner := r.tabOwner[t.TargetID]
		r.mu.Unlock()
		if m.cursorWaitsForHuman(owner) {
			continue
		}
		url, _ := t.Info()["url"].(string)
		if err := t.Close(ctx); err != nil {
			continue
		}
		open--
		r.mu.Lock()
		delete(r.tabOwner, t.TargetID)
		r.mu.Unlock()
		m.Bus.Publish("scheduler.tab_closed", id, owner, map[string]any{"target": t.TargetID, "url": url, "reason": "idle"})
	}
	r.mu.Lock()
	if open > 0 {
		r.noTabs = time.Time{}
		r.mu.Unlock()
		return
	}
	if r.noTabs.IsZero() {
		r.noTabs = time.Now()
	}
	stop := time.Since(r.noTabs) > min(browserIdle, m.cfg.TabIdle)
	r.mu.Unlock()
	if !stop {
		return
	}
	r.brMu.Lock()
	if r.browser != b {
		r.brMu.Unlock()
		return
	}
	r.browser = nil
	r.brMu.Unlock()
	r.mu.Lock()
	r.noTabs = time.Time{}
	r.lastTabs = nil
	r.mu.Unlock()
	b.Close(ctx)
	m.Bus.Publish("scheduler.browser_stopped", id, "", map[string]any{"reason": "no tabs"})
}

const browserIdle = 2 * time.Minute

const pendingTTL = 24 * time.Hour

func (m *Manager) reapCursors() {
	var stale []string
	m.mu.Lock()
	for id, c := range m.cursors {
		if c.Status == CursorPending && c.Launch == nil && c.Labels["paseo_agent"] == "" && time.Since(c.Updated) > pendingTTL {
			stale = append(stale, id)
		}
	}
	m.mu.Unlock()
	for _, id := range stale {
		m.CancelCursor(id, "never started", false)
	}
	m.pruneCursors(m.cfg.CursorRetention)
}

func (m *Manager) pruneCursors(retention time.Duration) {
	if retention <= 0 {
		return
	}
	old := map[string]string{}
	m.mu.Lock()
	busy := map[string]bool{}
	for _, c := range m.cursors {
		if c.Status.Terminal() {
			continue
		}
		p := c.Parent
		for i := 0; i < 32 && p != "" && !busy[p]; i++ {
			busy[p] = true
			pc := m.cursors[p]
			if pc == nil {
				break
			}
			p = pc.Parent
		}
	}
	for id, c := range m.cursors {
		if !c.Status.Terminal() || busy[id] {
			continue
		}
		last := c.Updated
		if c.Ended != nil && c.Ended.After(last) {
			last = *c.Ended
		}
		if time.Since(last) > retention {
			old[id] = c.Workspace
		}
	}
	m.mu.Unlock()
	touched := map[string]bool{}
	for id, ws := range old {
		if m.DeleteCursor(id) == nil && ws != "" {
			touched[ws] = true
		}
	}
	for ws := range touched {
		m.mu.Lock()
		_, known := m.ws[ws]
		running := m.rt[ws] != nil
		m.mu.Unlock()
		if !known {
			m.Bus.RemoveLog(ws)
		} else if !running {
			m.Bus.CloseLog(ws)
		}
	}
}

func (m *Manager) Asleep(ws string) bool {
	if r := m.runtime(ws); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.closing
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.ws[ws]
	return w != nil && (w.State == StateSuspended || w.State == StateCreated)
}

func (m *Manager) ownerActive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cursors[id]
	return c != nil && !c.Status.Terminal()
}

func (m *Manager) cursorWaitsForHuman(id string) bool {
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cursors[id]
	return c != nil && (c.Status == CursorNeedHuman || c.Status == CursorBlocked)
}

func (m *Manager) hasActiveAgents(ws string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.cursors {
		if c.Workspace == ws && !c.Status.Terminal() && c.Status != CursorPaused && c.Kind != "human" {
			if time.Since(c.Updated) < 10*time.Minute {
				return true
			}
		}
	}
	return false
}

func (m *Manager) usageTick() {
	m.mu.Lock()
	rts := make(map[string]*Runtime, len(m.rt))
	for id, r := range m.rt {
		rts[id] = r
	}
	m.mu.Unlock()
	for id, r := range rts {
		r.mu.Lock()
		frozen := r.frozen
		r.mu.Unlock()
		if frozen {
			continue
		}
		u := m.measureLight(id, r)
		m.mu.Lock()
		if prev := m.usage[id]; prev != nil && prev.Detailed && time.Since(prev.Time) < time.Minute {
			prev.CPU, prev.CgroupMB, prev.RSSMB, prev.Procs = u.CPU, u.CgroupMB, u.RSSMB, u.Procs
			u = prev
		} else if m.rt[id] == r {
			m.usage[id] = u
		}
		w := m.ws[id]
		var budget Budget
		if w != nil {
			budget = w.Config.Budget
		}
		m.mu.Unlock()
		mem := u.CgroupMB
		if mem == 0 {
			mem = u.RSSMB
		}
		if budget.MemoryMB > 0 && mem > float64(budget.MemoryMB) {
			m.Bus.Publish("workspace.budget_exceeded", id, "", map[string]any{"resource": "memory", "used_mb": int(mem), "limit_mb": budget.MemoryMB})
			go m.relieve(id, r, mem > float64(budget.MemoryMB)*1.25)
		}
		if budget.CPUPercent > 0 && u.CPU > float64(budget.CPUPercent) && m.cg == nil {
			m.Bus.Publish("workspace.budget_exceeded", id, "", map[string]any{"resource": "cpu", "used": u.CPU, "limit": budget.CPUPercent})
		}
	}
}

func (m *Manager) measureLight(id string, r *Runtime) *WorkspaceUsage {
	u := &WorkspaceUsage{Time: time.Now().UTC(), ByRole: map[string]float64{}}
	var ticks map[int]uint64
	if m.cg != nil {
		u.CgroupMB = float64(m.cg.Memory(id)) / (1 << 20)
		usec := m.cg.CPUUsec(id)
		u.Procs = len(m.cg.Pids(id))
		m.mu.Lock()
		prev, ok := m.cpuPrev[id]
		if m.rt[id] == r {
			m.cpuPrev[id] = cpuSample{t: time.Now(), usec: usec}
		}
		m.mu.Unlock()
		if ok && usec >= prev.usec {
			u.CPU = float64(usec-prev.usec) / 1e6 / time.Since(prev.t).Seconds() * 100
		}
		return u
	}
	pids := r.allPids()
	u.Procs = len(pids)
	ticks = map[int]uint64{}
	for _, p := range pids {
		t, rss, err := StatLight(p)
		if err != nil {
			continue
		}
		ticks[p] = t
		u.RSSMB += float64(rss) / 1024
	}
	m.mu.Lock()
	prev, ok := m.cpuPrev[id]
	if m.rt[id] == r {
		m.cpuPrev[id] = cpuSample{t: time.Now(), ticks: ticks}
	}
	m.mu.Unlock()
	if ok {
		el := time.Since(prev.t).Seconds()
		var d uint64
		for p, t := range ticks {
			if pt, ok := prev.ticks[p]; ok && t >= pt {
				d += t - pt
			}
		}
		u.CPU = float64(d) / clkTck / el * 100
	}
	return u
}

func (m *Manager) DetailedUsage(id string) *WorkspaceUsage {
	r := m.runtime(id)
	if r == nil {
		return nil
	}
	r.mu.Lock()
	frozen := r.frozen
	r.mu.Unlock()
	m.mu.Lock()
	cur := m.usage[id]
	m.mu.Unlock()
	if frozen || (cur != nil && cur.Detailed && time.Since(cur.Time) < 30*time.Second) {
		return cur
	}
	u := m.measure(id, r)
	u.Detailed = true
	if cur != nil {
		u.CPU = cur.CPU
	}
	m.mu.Lock()
	m.usage[id] = u
	m.mu.Unlock()
	return u
}

func (m *Manager) measure(id string, r *Runtime) *WorkspaceUsage {
	pids := r.allPids()
	u := &WorkspaceUsage{Time: time.Now().UTC(), ByRole: map[string]float64{}}
	r.mu.Lock()
	u.Frozen = r.frozen
	xpid := 0
	if r.display != nil {
		xpid = r.display.Pid()
	}
	r.mu.Unlock()
	r.brMu.Lock()
	bpid := 0
	if r.browser != nil {
		bpid = r.browser.Pid()
	}
	r.brMu.Unlock()
	var all []ProcUsage
	for _, p := range pids {
		pu, err := ReadUsage(p)
		if err != nil {
			continue
		}
		role := pu.Role
		switch {
		case p == xpid:
			role = "xvfb"
		case p == bpid:
			role = "chrome-browser"
		case role == "" && (pu.Comm == "chrome" || pu.Comm == "chrome_crashpad"):
			role = "chrome-other"
		case role == "":
			role = "app:" + pu.Comm
		}
		pu.Role = role
		u.PSSMB += float64(pu.PSSKB) / 1024
		u.RSSMB += float64(pu.RSSKB) / 1024
		u.ByRole[role] += float64(pu.PSSKB) / 1024
		all = append(all, pu)
	}
	m.mu.Lock()
	prev, ok := m.cpuPrev[id]
	m.mu.Unlock()
	if ok {
		el := time.Since(prev.t).Seconds()
		for i := range all {
			if pt, ok := prev.ticks[all[i].Pid]; ok && all[i].CPUTicks >= pt {
				all[i].CPU = float64(all[i].CPUTicks-pt) / clkTck / el * 100
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].PSSKB > all[j].PSSKB })
	if len(all) > 8 {
		all = all[:8]
	}
	u.Top = all
	u.Procs = len(pids)
	if m.cg != nil {
		u.CgroupMB = float64(m.cg.Memory(id)) / (1 << 20)
	}
	return u
}

func (m *Manager) relieve(id string, r *Runtime, hard bool) {
	r.brMu.Lock()
	b := r.browser
	r.brMu.Unlock()
	if b != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tabs := b.Tabs()
		sort.Slice(tabs, func(i, j int) bool { return tabs[i].LastUsed().Before(tabs[j].LastUsed()) })
		for _, t := range tabs {
			r.mu.Lock()
			owner := r.tabOwner[t.TargetID]
			r.mu.Unlock()
			if owner == "" && time.Since(t.LastUsed()) > time.Minute {
				t.Close(ctx)
				m.Bus.Publish("scheduler.tab_closed", id, "", map[string]any{"target": t.TargetID, "reason": "memory budget"})
				break
			}
		}
	}
	if hard && r.claimSuspend() {
		m.Bus.Publish("scheduler.suspend", id, "", map[string]any{"reason": "memory budget exceeded by >25%"})
		m.suspendIfIdle(context.Background(), id, r, 0, "memory budget")
	}
}

type SystemInfo struct {
	Version    string                     `json:"version"`
	Uptime     string                     `json:"uptime"`
	Daemon     ProcUsage                  `json:"daemon"`
	Goroutines int                        `json:"goroutines"`
	HeapMB     float64                    `json:"heap_mb"`
	Workspaces map[string]int             `json:"workspaces"`
	Cursors    map[string]int             `json:"cursors"`
	Usage      map[string]*WorkspaceUsage `json:"usage"`
	TotalPSSMB float64                    `json:"total_pss_mb"`
	Cgroups    bool                       `json:"cgroups"`
	AppsUser   string                     `json:"apps_user"`
	EventSeq   int64                      `json:"event_seq"`
	LoadAvg    string                     `json:"loadavg"`
}

func (m *Manager) System() *SystemInfo {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := &SystemInfo{Version: Version, Uptime: time.Since(m.started).Round(time.Second).String(), Daemon: SelfUsage(), Goroutines: runtime.NumGoroutine(), HeapMB: float64(ms.HeapAlloc) / (1 << 20), Workspaces: map[string]int{}, Cursors: map[string]int{}, Usage: map[string]*WorkspaceUsage{}, Cgroups: m.cg != nil, AppsUser: m.cfg.AppsUser, EventSeq: m.Bus.Seq()}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		s.LoadAvg = string(b[:len(b)-1])
	}
	m.mu.Lock()
	for _, w := range m.ws {
		s.Workspaces[string(w.State)]++
	}
	for _, c := range m.cursors {
		s.Cursors[string(c.Status)]++
	}
	for id, u := range m.usage {
		s.Usage[id] = u
		s.TotalPSSMB += u.PSSMB
	}
	m.mu.Unlock()
	s.TotalPSSMB += float64(s.Daemon.PSSKB) / 1024
	return s
}
