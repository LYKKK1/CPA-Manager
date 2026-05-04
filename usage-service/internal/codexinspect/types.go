package codexinspect

import "time"

const (
	ActionKeep    = "keep"
	ActionDelete  = "delete"
	ActionDisable = "disable"
	ActionEnable  = "enable"
)

type ScheduleConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"intervalMinutes"`
	AutoToggle      bool `json:"autoToggle"`
}

type RuntimeConfig struct {
	BaseURL       string
	ManagementKey string
}

type Settings struct {
	TargetType           string
	Workers              int
	DelaySeconds         int
	Timeout              time.Duration
	UserAgent            string
	UsedPercentThreshold float64
}

type AuthFile struct {
	Name      string         `json:"name"`
	Type      string         `json:"type,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	AuthIndex any            `json:"authIndex,omitempty"`
	Disabled  bool           `json:"disabled,omitempty"`
	Status    string         `json:"status,omitempty"`
	Raw       map[string]any `json:"-"`
}

type ResultItem struct {
	FileName       string   `json:"fileName"`
	DisplayAccount string   `json:"displayAccount"`
	AuthIndex      string   `json:"authIndex,omitempty"`
	AccountID      string   `json:"accountId,omitempty"`
	Provider       string   `json:"provider"`
	Disabled       bool     `json:"disabled"`
	Action         string   `json:"action"`
	ActionReason   string   `json:"actionReason"`
	StatusCode     *int     `json:"statusCode,omitempty"`
	UsedPercent    *float64 `json:"usedPercent,omitempty"`
	IsQuota        bool     `json:"isQuota"`
	Error          string   `json:"error,omitempty"`
}

type ExecutionOutcome struct {
	Action         string `json:"action"`
	FileName       string `json:"fileName"`
	DisplayAccount string `json:"displayAccount"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
}

type Summary struct {
	TotalFiles     int      `json:"totalFiles"`
	ProbeSetCount  int      `json:"probeSetCount"`
	SampledCount   int      `json:"sampledCount"`
	DeleteCount    int      `json:"deleteCount"`
	DisableCount   int      `json:"disableCount"`
	EnableCount    int      `json:"enableCount"`
	KeepCount      int      `json:"keepCount"`
	AutoDisabled   int      `json:"autoDisabled"`
	AutoEnabled    int      `json:"autoEnabled"`
	AutoFailed     int      `json:"autoFailed"`
	SkippedDeletes int      `json:"skippedDeletes"`
	Messages       []string `json:"messages,omitempty"`
}

type RunResult struct {
	Settings   Settings           `json:"settings"`
	Schedule   ScheduleConfig     `json:"schedule"`
	Results    []ResultItem       `json:"results"`
	Outcomes   []ExecutionOutcome `json:"outcomes,omitempty"`
	Summary    Summary            `json:"summary"`
	StartedAt  int64              `json:"startedAt"`
	FinishedAt int64              `json:"finishedAt"`
	Error      string             `json:"error,omitempty"`
}

type Status struct {
	Schedule  ScheduleConfig `json:"schedule"`
	Running   bool           `json:"running"`
	LastRunAt int64          `json:"lastRunAt,omitempty"`
	NextRunAt int64          `json:"nextRunAt,omitempty"`
	LastError string         `json:"lastError,omitempty"`
	LastRun   *RunResult     `json:"lastRun,omitempty"`
}
