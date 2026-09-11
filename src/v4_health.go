package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	healthClosed   = "closed"
	healthOpen     = "open"
	healthHalfOpen = "half-open"
)

const (
	healthInitialCooldownV4      = 30 * time.Second
	healthRateLimitCooldownV4    = 60 * time.Second
	healthUnauthorizedCooldownV4 = 5 * time.Minute
	healthMaxCooldownV4          = 5 * time.Minute
)

type candidateHealthState struct {
	State               string
	ConsecutiveFailures int
	BackoffLevel        int
	LastStatus          int
	LastError           string
	LastFailureAt       time.Time
	LastSuccessAt       time.Time
	OpenedAt            time.Time
	NextProbeAt         time.Time
	ProbeInFlight       bool
}

type candidateHealthSnapshot struct {
	PolicyName          string `json:"policy_name,omitempty"`
	KeyFingerprint      string `json:"key_fingerprint,omitempty"`
	RuleID              string `json:"rule_id,omitempty"`
	RuleName            string `json:"rule_name,omitempty"`
	CandidateID         string `json:"candidate_id,omitempty"`
	CandidateName       string `json:"candidate_name,omitempty"`
	Provider            string `json:"provider,omitempty"`
	AuthIndex           string `json:"auth_index,omitempty"`
	State               string `json:"state"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	BackoffLevel        int    `json:"backoff_level"`
	LastStatus          int    `json:"last_status,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	LastFailureAt       string `json:"last_failure_at,omitempty"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	OpenedAt            string `json:"opened_at,omitempty"`
	NextProbeAt         string `json:"next_probe_at,omitempty"`
	RetryInMs           int64  `json:"retry_in_ms,omitempty"`
	ProbeInFlight       bool   `json:"probe_in_flight"`
}

func candidateHealthDefaultsV4() map[string]any {
	return map[string]any{
		"enabled":                       true,
		"failure_threshold":             1,
		"success_threshold":             1,
		"initial_cooldown_seconds":      int(healthInitialCooldownV4 / time.Second),
		"rate_limit_cooldown_seconds":   int(healthRateLimitCooldownV4 / time.Second),
		"unauthorized_cooldown_seconds": int(healthUnauthorizedCooldownV4 / time.Second),
		"max_cooldown_seconds":          int(healthMaxCooldownV4 / time.Second),
		"backoff_multiplier":            2,
		"half_open_max_probes":          1,
	}
}

func candidateHealthKeyV4(p *Policy, r *PolicyRule, c *PolicyCandidate) string {
	if p == nil || r == nil || c == nil {
		return ""
	}
	return strings.TrimSpace(p.KeyFingerprint) + "\x00" + strings.TrimSpace(r.ID) + "\x00" + strings.TrimSpace(c.ID)
}

func candidateHealthStateLockedV4(key string) *candidateHealthState {
	if key == "" {
		return nil
	}
	if v4Runtime.health == nil {
		v4Runtime.health = map[string]*candidateHealthState{}
	}
	h := v4Runtime.health[key]
	if h == nil {
		h = &candidateHealthState{State: healthClosed}
		v4Runtime.health[key] = h
	}
	if h.State == "" {
		h.State = healthClosed
	}
	return h
}

func candidateWouldBeSelectableV4(p *Policy, r *PolicyRule, c *PolicyCandidate, now time.Time) bool {
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return true
	}
	v4Runtime.RLock()
	h := v4Runtime.health[key]
	if h == nil || h.State == "" || h.State == healthClosed {
		v4Runtime.RUnlock()
		return true
	}
	state := h.State
	nextProbe := h.NextProbeAt
	inFlight := h.ProbeInFlight
	v4Runtime.RUnlock()
	if inFlight {
		return false
	}
	if state == healthOpen {
		return nextProbe.IsZero() || !now.Before(nextProbe)
	}
	return state == healthHalfOpen
}

func acquireCandidateHealthV4(p *Policy, r *PolicyRule, c *PolicyCandidate, now time.Time) (bool, bool) {
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return true, false
	}
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	h := candidateHealthStateLockedV4(key)
	if h.State == healthClosed {
		return true, false
	}
	if h.ProbeInFlight {
		return false, false
	}
	if h.State == healthOpen && !h.NextProbeAt.IsZero() && now.Before(h.NextProbeAt) {
		return false, false
	}
	h.State = healthHalfOpen
	h.ProbeInFlight = true
	return true, true
}

func candidateMatchesFailoverActionV4(c *PolicyCandidate, action string, currentPriority int, phase int) bool {
	if c == nil {
		return false
	}
	switch action {
	case failNextPriority:
		return c.Priority < currentPriority
	case failSamePriorityFirst:
		if phase == 0 {
			return c.Priority == currentPriority
		}
		return c.Priority < currentPriority
	default:
		return true
	}
}

func chooseHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int, acquire bool) (*PolicyCandidate, bool) {
	phases := 1
	if action == failSamePriorityFirst {
		phases = 2
	}
	now := time.Now()
	for phase := 0; phase < phases; phase++ {
		for _, c := range ranked {
			if c == nil || attempted[c.ID] || !candidateMatchesFailoverActionV4(c, action, currentPriority, phase) {
				continue
			}
			if acquire {
				if ok, probe := acquireCandidateHealthV4(p, r, c, now); ok {
					return c, probe
				}
			} else if candidateWouldBeSelectableV4(p, r, c, now) {
				return c, false
			}
		}
	}
	return nil, false
}

func nextHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) (*PolicyCandidate, bool) {
	return chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, true)
}

func peekHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) *PolicyCandidate {
	c, _ := chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, false)
	return c
}

func retryAfterDurationV4(headers http.Header, now time.Time) time.Duration {
	if headers == nil {
		return 0
	}
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

func candidateFailureCooldownV4(status int, err error, headers http.Header, backoffLevel int, now time.Time) (time.Duration, bool) {
	base := time.Duration(0)
	switch {
	case status == 0 && err != nil:
		base = healthInitialCooldownV4
	case status == 401 || status == 403:
		base = healthUnauthorizedCooldownV4
	case status == 408 || status == 409:
		base = healthInitialCooldownV4
	case status == 429:
		base = healthRateLimitCooldownV4
	case status >= 500 && status <= 599:
		base = healthInitialCooldownV4
	default:
		return 0, false
	}

	cooldown := base
	for i := 0; i < backoffLevel && cooldown < healthMaxCooldownV4; i++ {
		cooldown *= 2
		if cooldown > healthMaxCooldownV4 {
			cooldown = healthMaxCooldownV4
		}
	}
	if status == 429 {
		if retryAfter := retryAfterDurationV4(headers, now); retryAfter > cooldown {
			cooldown = retryAfter
		}
	}
	return cooldown, true
}

func recordCandidateFailureV4(p *Policy, r *PolicyRule, c *PolicyCandidate, status int, err error, headers http.Header, probeAttempt ...bool) {
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return
	}
	now := time.Now()
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	h := candidateHealthStateLockedV4(key)
	isProbe := len(probeAttempt) > 0 && probeAttempt[0]
	level := h.BackoffLevel
	if isProbe && level < 30 {
		level++
	}
	cooldown, qualifies := candidateFailureCooldownV4(status, err, headers, level, now)
	if !qualifies {
		// A half-open probe that reaches the upstream and receives a request-specific
		// non-health status proves transport reachability; do not strand the lease.
		if isProbe && (h.ProbeInFlight || h.State == healthHalfOpen) {
			h.State = healthClosed
			h.ProbeInFlight = false
			h.ConsecutiveFailures = 0
			h.BackoffLevel = 0
			h.NextProbeAt = time.Time{}
			h.OpenedAt = time.Time{}
		}
		return
	}

	proposedDeadline := now.Add(cooldown)
	wasClosed := h.State == "" || h.State == healthClosed
	switch {
	case isProbe:
		// Only a failed half-open recovery probe advances exponential backoff.
		h.BackoffLevel = level
		h.State = healthOpen
		h.ProbeInFlight = false
		h.OpenedAt = now
		h.NextProbeAt = proposedDeadline
	case wasClosed:
		// First failure opens the circuit at the base cooldown. Concurrent attempts
		// that were already in flight must not escalate the backoff level.
		h.State = healthOpen
		h.ProbeInFlight = false
		h.OpenedAt = now
		h.NextProbeAt = proposedDeadline
	default:
		// This is a stale normal attempt that started before another request opened
		// the circuit (or while a recovery probe is now running). Do not alter the
		// state, probe lease, or backoff. Only stronger auth/rate-limit deadlines may
		// extend an existing open deadline; never shorten it.
		if status == 401 || status == 403 || status == 429 {
			if h.NextProbeAt.IsZero() || proposedDeadline.After(h.NextProbeAt) {
				h.NextProbeAt = proposedDeadline
			}
		}
	}

	h.ConsecutiveFailures++
	h.LastStatus = status
	h.LastError = ""
	if err != nil {
		h.LastError = err.Error()
	}
	h.LastFailureAt = now
}

func recordCandidateSuccessV4(p *Policy, r *PolicyRule, c *PolicyCandidate) {
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return
	}
	now := time.Now()
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	h := candidateHealthStateLockedV4(key)
	h.State = healthClosed
	h.ProbeInFlight = false
	h.ConsecutiveFailures = 0
	h.BackoffLevel = 0
	h.LastStatus = 0
	h.LastError = ""
	h.LastSuccessAt = now
	h.OpenedAt = time.Time{}
	h.NextProbeAt = time.Time{}
}

func releaseCandidateProbeV4(p *Policy, r *PolicyRule, c *PolicyCandidate) {
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return
	}
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	h := v4Runtime.health[key]
	if h == nil || !h.ProbeInFlight {
		return
	}
	h.ProbeInFlight = false
	h.State = healthOpen
	h.NextProbeAt = time.Now()
}

func healthSnapshotFromStateV4(p *Policy, r *PolicyRule, c *PolicyCandidate, h *candidateHealthState, now time.Time) candidateHealthSnapshot {
	out := candidateHealthSnapshot{
		PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, RuleID: r.ID, RuleName: r.Name,
		CandidateID: c.ID, CandidateName: c.Name, Provider: c.Provider, AuthIndex: c.AuthIndex,
		State: healthClosed,
	}
	if h == nil {
		return out
	}
	out.State = h.State
	if out.State == "" {
		out.State = healthClosed
	}
	out.ConsecutiveFailures = h.ConsecutiveFailures
	out.BackoffLevel = h.BackoffLevel
	out.LastStatus = h.LastStatus
	out.LastError = h.LastError
	out.ProbeInFlight = h.ProbeInFlight
	if !h.LastFailureAt.IsZero() {
		out.LastFailureAt = h.LastFailureAt.UTC().Format(time.RFC3339Nano)
	}
	if !h.LastSuccessAt.IsZero() {
		out.LastSuccessAt = h.LastSuccessAt.UTC().Format(time.RFC3339Nano)
	}
	if !h.OpenedAt.IsZero() {
		out.OpenedAt = h.OpenedAt.UTC().Format(time.RFC3339Nano)
	}
	if !h.NextProbeAt.IsZero() {
		out.NextProbeAt = h.NextProbeAt.UTC().Format(time.RFC3339Nano)
		if h.NextProbeAt.After(now) {
			out.RetryInMs = h.NextProbeAt.Sub(now).Milliseconds()
		}
	}
	return out
}

func candidateHealthViewV4(p *Policy, r *PolicyRule, c *PolicyCandidate) candidateHealthSnapshot {
	key := candidateHealthKeyV4(p, r, c)
	now := time.Now()
	v4Runtime.RLock()
	h := v4Runtime.health[key]
	var copyState *candidateHealthState
	if h != nil {
		x := *h
		copyState = &x
	}
	v4Runtime.RUnlock()
	return healthSnapshotFromStateV4(p, r, c, copyState, now)
}

func candidateHealthSnapshotV4() []candidateHealthSnapshot {
	now := time.Now()
	v4Runtime.RLock()
	out := make([]candidateHealthSnapshot, 0)
	for _, p := range v4Runtime.state.Policies {
		if p == nil {
			continue
		}
		for _, r := range p.Rules {
			if r == nil {
				continue
			}
			for _, c := range r.Candidates {
				if c == nil || !c.Enabled {
					continue
				}
				key := candidateHealthKeyV4(p, r, c)
				var copyState *candidateHealthState
				if h := v4Runtime.health[key]; h != nil {
					x := *h
					copyState = &x
				}
				out = append(out, healthSnapshotFromStateV4(p, r, c, copyState, now))
			}
		}
	}
	v4Runtime.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].PolicyName != out[j].PolicyName {
			return out[i].PolicyName < out[j].PolicyName
		}
		if out[i].RuleName != out[j].RuleName {
			return out[i].RuleName < out[j].RuleName
		}
		return out[i].CandidateName < out[j].CandidateName
	})
	return out
}

func pruneCandidateHealthLockedV4() {
	if len(v4Runtime.health) == 0 {
		return
	}
	valid := make(map[string]struct{})
	for _, p := range v4Runtime.state.Policies {
		if p == nil {
			continue
		}
		for _, r := range p.Rules {
			if r == nil {
				continue
			}
			for _, c := range r.Candidates {
				if c == nil {
					continue
				}
				if key := candidateHealthKeyV4(p, r, c); key != "" {
					valid[key] = struct{}{}
				}
			}
		}
	}
	for key := range v4Runtime.health {
		if _, ok := valid[key]; !ok {
			delete(v4Runtime.health, key)
		}
	}
}
