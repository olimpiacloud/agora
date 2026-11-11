package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type WSState string

const (
	StateCreated   WSState = "created"
	StateActive    WSState = "active"
	StateIdle      WSState = "idle"
	StateFrozen    WSState = "frozen"
	StateSuspended WSState = "suspended"
	StateDestroyed WSState = "destroyed"
)

type Budget struct {
	MemoryMB   int `json:"memory_mb,omitempty"`
	CPUPercent int `json:"cpu_percent,omitempty"`
	MaxCursors int `json:"max_cursors,omitempty"`
	MaxTabs    int `json:"max_tabs,omitempty"`
	MaxDepth   int `json:"max_depth,omitempty"`
}

type AppSpec struct {
	ID      string   `json:"id"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Restore bool     `json:"restore,omitempty"`
	Owner   string   `json:"owner,omitempty"`
}

type WorkspaceConfig struct {
	Width         int      `json:"width"`
	Height        int      `json:"height"`
	Profile       string   `json:"profile"`
	Layout        string   `json:"layout"`
	Headless      bool     `json:"headless,omitempty"`
	IdleAfter     Duration `json:"idle_after,omitempty"`
	SuspendAfter  Duration `json:"suspend_after,omitempty"`
	FreezeOnIdle  bool     `json:"freeze_on_idle,omitempty"`
	TTL           Duration `json:"ttl,omitempty"`
	Budget        Budget   `json:"budget"`
	BrowserArgs   []string `json:"browser_args,omitempty"`
	AutostartURLs []string `json:"autostart_urls,omitempty"`
	Sandbox       bool     `json:"sandbox,omitempty"`
	WebGL         string   `json:"webgl,omitempty"`
}

type TabRecord struct {
	Target  string `json:"target"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Owner   string `json:"owner,omitempty"`
	Context string `json:"context,omitempty"`
}

type Checkpoint struct {
	ID       string      `json:"id"`
	Time     time.Time   `json:"time"`
	Reason   string      `json:"reason"`
	Tabs     []TabRecord `json:"tabs"`
	Apps     []AppSpec   `json:"apps"`
	Windows  int         `json:"windows"`
	Cursors  []string    `json:"cursors"`
	Snapshot string      `json:"snapshot,omitempty"`
	Layout   string      `json:"layout"`
	Width    int         `json:"width"`
	Height   int         `json:"height"`
}

type Workspace struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	State      WSState           `json:"state"`
	Config     WorkspaceConfig   `json:"config"`
	Owner      string            `json:"owner,omitempty"`
	OwnerRoot  string            `json:"owner_root,omitempty"`
	ForkOf     string            `json:"fork_of,omitempty"`
	Created    time.Time         `json:"created"`
	Updated    time.Time         `json:"updated"`
	LastActive time.Time         `json:"last_active"`
	Checkpoint *Checkpoint       `json:"checkpoint,omitempty"`
	Apps       []AppSpec         `json:"apps,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type CursorStatus string

const (
	CursorPending   CursorStatus = "pending"
	CursorRunning   CursorStatus = "running"
	CursorThinking  CursorStatus = "thinking"
	CursorExecuting CursorStatus = "executing"
	CursorWaiting   CursorStatus = "waiting"
	CursorBlocked   CursorStatus = "blocked"
	CursorNeedHuman CursorStatus = "needs_human"
	CursorPaused    CursorStatus = "paused"
	CursorDone      CursorStatus = "done"
	CursorFailed    CursorStatus = "failed"
	CursorCancelled CursorStatus = "cancelled"
)

func (s CursorStatus) Terminal() bool {
	return s == CursorDone || s == CursorFailed || s == CursorCancelled
}

type Progress struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
	Percent int       `json:"percent,omitempty"`
}

type Message struct {
	Seq  int64           `json:"seq"`
	Time time.Time       `json:"time"`
	From string          `json:"from"`
	To   string          `json:"to"`
	Body json.RawMessage `json:"body"`
}

type Usage struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	Turns            int     `json:"turns"`
	DurationMs       int64   `json:"duration_ms"`
}

type LaunchSpec struct {
	Profile    string            `json:"profile,omitempty"`
	Effort     string            `json:"effort,omitempty"`
	Tools      []string          `json:"tools,omitempty"`
	Adapter    string            `json:"adapter"`
	Prompt     string            `json:"prompt,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Model      string            `json:"model,omitempty"`
	MaxRuntime Duration          `json:"max_runtime,omitempty"`
}

type Cursor struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Parent      string            `json:"parent,omitempty"`
	Root        string            `json:"root"`
	Depth       int               `json:"depth"`
	Agent       string            `json:"agent,omitempty"`
	Goal        string            `json:"goal,omitempty"`
	Status      CursorStatus      `json:"status"`
	Detail      string            `json:"detail,omitempty"`
	Workspace   string            `json:"workspace,omitempty"`
	Surface     string            `json:"surface,omitempty"`
	Permissions []string          `json:"permissions"`
	Budget      Budget            `json:"budget"`
	Color       string            `json:"color"`
	X           int               `json:"x"`
	Y           int               `json:"y"`
	Result      json.RawMessage   `json:"result,omitempty"`
	Error       string            `json:"error,omitempty"`
	Progress    []Progress        `json:"progress,omitempty"`
	Actions     int64             `json:"actions"`
	LastAction  string            `json:"last_action,omitempty"`
	Created     time.Time         `json:"created"`
	Updated     time.Time         `json:"updated"`
	Ended       *time.Time        `json:"ended,omitempty"`
	TokenHash   string            `json:"token_hash,omitempty"`
	Worktree    string            `json:"worktree,omitempty"`
	Launch      *LaunchSpec       `json:"launch,omitempty"`
	LaunchPid   int               `json:"launch_pid,omitempty"`
	Usage       *Usage            `json:"usage,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

func (c *Cursor) Public() *Cursor {
	if c == nil {
		return nil
	}
	cp := *c
	cp.TokenHash = ""
	if c.Launch != nil {
		l := *c.Launch
		l.Env = nil
		cp.Launch = &l
	}
	return &cp
}

func PublicCursors(cs []*Cursor) []*Cursor {
	out := make([]*Cursor, len(cs))
	for i, c := range cs {
		out[i] = c.Public()
	}
	return out
}

type Worktree struct {
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	Base      string    `json:"base"`
	Cursor    string    `json:"cursor,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	Root      string    `json:"root,omitempty"`
	PortBase  int       `json:"port_base"`
	Ports     int       `json:"ports"`
	Created   time.Time `json:"created"`
}

type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration == 0 {
		return []byte(`""`), nil
	}
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		var n float64
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return err
		}
		d.Duration = time.Duration(n * float64(time.Second))
		return nil
	}
	if s == "" {
		d.Duration = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func NewID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func NewSecret() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var palette = []string{"#ff5d5d", "#4fb3ff", "#ffb547", "#57d98b", "#c77dff", "#ff7ac6", "#3ee0d0", "#f5e663", "#ff8f4f", "#8aa1ff"}
