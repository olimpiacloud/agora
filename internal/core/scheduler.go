package core

import (
	"context"
	"os"
	"runtime"
	"sort"
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
}

type cpuSample struct {
	t     time.Time
	ticks map[int]uint64
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
		if frozen {
			if it.w.Config.SuspendAfter.Duration > 0 && idleFor > it.w.Config.SuspendAfter.Duration {
				go m.Suspend(context.Background(), it.w.ID, "idle")
			}
			continue
		}
		r.releaseSeats(m.cfg.SeatIdle)
		if viewers > 0 {
			continue
		}
		if m.hasActiveAgents(it.w.ID) && idleFor < 30*time.Minute {
			continue
		}
		if !idle && it.w.Config.IdleAfter.Duration > 0 && idleFor > it.w.Config.IdleAfter.Duration {
			r.mu.Lock()
			r.idle = true
			r.mu.Unlock()
			r.releaseSeats(0)
			m.setState(it.w.ID, StateIdle, "no activity")
			if it.w.Config.FreezeOnIdle {
				go m.Freeze(context.Background(), it.w.ID)
			}
			continue
		}
		if it.w.Config.SuspendAfter.Duration > 0 && idleFor > it.w.Config.SuspendAfter.Duration {
			go m.Suspend(context.Background(), it.w.ID, "idle")
		}
	}
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
		u := m.measure(id, r)
		m.mu.Lock()
		m.usage[id] = u
		w := m.ws[id]
		var budget Budget
		if w != nil {
			budget = w.Config.Budget
		}
		m.mu.Unlock()
		if budget.MemoryMB > 0 && u.PSSMB > float64(budget.MemoryMB) {
			m.Bus.Publish("workspace.budget_exceeded", id, "", map[string]any{"resource": "memory", "used_mb": int(u.PSSMB), "limit_mb": budget.MemoryMB})
			go m.relieve(id, r, u.PSSMB > float64(budget.MemoryMB)*1.25)
		}
		if budget.CPUPercent > 0 && u.CPU > float64(budget.CPUPercent) && m.cg == nil {
			m.Bus.Publish("workspace.budget_exceeded", id, "", map[string]any{"resource": "cpu", "used": u.CPU, "limit": budget.CPUPercent})
		}
	}
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
	ticks := map[int]uint64{}
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
		ticks[p] = pu.CPUTicks
		all = append(all, pu)
	}
	m.mu.Lock()
	prev, ok := m.cpuPrev[id]
	m.cpuPrev[id] = cpuSample{t: time.Now(), ticks: ticks}
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
	if hard {
		m.Bus.Publish("scheduler.suspend", id, "", map[string]any{"reason": "memory budget exceeded by >25%"})
		m.Suspend(context.Background(), id, "memory budget")
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

var Version = "0.1.0"

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
