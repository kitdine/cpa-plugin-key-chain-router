package main

import (
	"database/sql"
	"sync"
)

const v4StateVersion = 4

const (
	decisionHandled       = "KCR_HANDLED"
	decisionBypass        = "KCR_BYPASS_CPA_DEFAULT"
	decisionFallbackToCPA = "KCR_FALLBACK_TO_CPA"
)

const (
	strategyOrdered          = "ordered-failover"
	strategyRoundRobin       = "round-robin"
	strategyWeightedRR       = "weighted-round-robin"
	strategyPriorityWeighted = "priority-weighted"
	strategySticky           = "sticky"
	strategyCPADefault       = "cpa-default"
)

const (
	failNext              = "next"
	failSamePriorityFirst = "same-priority-first"
	failNextPriority      = "next-priority"
	failStop              = "stop"
	failCPADefault        = "cpa-default"
)

type V4State struct {
	Version       int                 `json:"version"`
	UpdatedAt     string              `json:"updated_at"`
	Policies      map[string]*Policy  `json:"policies"`
	Observability ObservabilityConfig `json:"observability"`
}

type Policy struct {
	Name           string        `json:"name"`
	KeyFingerprint string        `json:"key_fingerprint"`
	KeyHint        string        `json:"key_hint"`
	Enabled        bool          `json:"enabled"`
	Rules          []*PolicyRule `json:"rules"`
}

type PolicyRule struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Models       []string           `json:"models"`
	Strategy     string             `json:"strategy"`
	StickySource string             `json:"sticky_source,omitempty"`
	StickyHeader string             `json:"sticky_header,omitempty"`
	Candidates   []*PolicyCandidate `json:"candidates,omitempty"`
	Failover     FailoverPolicy     `json:"failover"`
}

type PolicyCandidate struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ResourceID    string `json:"resource_id,omitempty"`
	ResourceKind  string `json:"resource_kind,omitempty"`
	Provider      string `json:"provider"`
	AuthID        string `json:"auth_id,omitempty"`
	AuthIndex     string `json:"auth_index,omitempty"`
	OverrideModel string `json:"override_model,omitempty"`
	Enabled       bool   `json:"enabled"`
	Priority      int    `json:"priority"`
	Weight        int    `json:"weight"`
}

type FailoverPolicy struct {
	Network      string `json:"network"`
	Unauthorized string `json:"unauthorized"`
	Timeout      string `json:"timeout"`
	Conflict     string `json:"conflict"`
	RateLimit    string `json:"rate_limit"`
	ServerError  string `json:"server_error"`
	Other        string `json:"other"`
	Exhausted    string `json:"exhausted"`
	MaxAttempts  int    `json:"max_attempts"`
}

type ObservabilityConfig struct {
	MemoryEnabled       bool   `json:"memory_enabled"`
	MemoryLimit         int    `json:"memory_limit"`
	LogEnabled          bool   `json:"log_enabled"`
	LogLevel            string `json:"log_level"`
	SQLiteEnabled       bool   `json:"sqlite_enabled"`
	SQLitePath          string `json:"sqlite_path"`
	SQLiteRetentionDays int    `json:"sqlite_retention_days"`
	SQLiteMaxRows       int    `json:"sqlite_max_rows"`
	ResponseHeaders     bool   `json:"response_headers"`
}

type RoutingEvent struct {
	TraceID         string          `json:"trace_id"`
	At              string          `json:"at"`
	Decision        string          `json:"decision"`
	Reason          string          `json:"reason,omitempty"`
	PolicyName      string          `json:"policy_name,omitempty"`
	KeyFingerprint  string          `json:"key_fingerprint,omitempty"`
	KeyHint         string          `json:"key_hint,omitempty"`
	RuleID          string          `json:"rule_id,omitempty"`
	RuleName        string          `json:"rule_name,omitempty"`
	Strategy        string          `json:"strategy,omitempty"`
	Model           string          `json:"model,omitempty"`
	Stream          bool            `json:"stream"`
	Attempts        []attemptResult `json:"attempts,omitempty"`
	Final           string          `json:"final,omitempty"`
	Provider        string          `json:"provider,omitempty"`
	AuthIndex       string          `json:"auth_index,omitempty"`
	Status          int             `json:"status,omitempty"`
	DurationMs      int64           `json:"duration_ms,omitempty"`
	Success         bool            `json:"success"`
}

type sqliteSink struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
	ch   chan RoutingEvent
	stop chan struct{}
	done chan struct{}
	cfg  ObservabilityConfig
}

var v4Runtime = struct {
	sync.RWMutex
	state  V4State
	recent []RoutingEvent
	rr     map[string]uint64
	smooth map[string]map[string]int
	sqlite *sqliteSink
}{
	rr:     map[string]uint64{},
	smooth: map[string]map[string]int{},
}
