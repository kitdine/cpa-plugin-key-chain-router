package main

import (
	"fmt"
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
	if !candidateHealthConfigCurrentLockedV4(p, r, c) {
		v4Runtime.RUnlock()
		return true
	}
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

func acquireCandidateHealthWithReasonV4(p *Policy, r *PolicyRule, c *PolicyCandidate, now time.Time) (bool, bool, uint64, string) {
	key := candidateHealthKeyV4(p, r, c)
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	generation := v4Runtime.generation
	if key == "" {
		return true, false, generation, ""
	}
	if !candidateHealthConfigCurrentLockedV4(p, r, c) {
		return true, false, generation, ""
	}
	h := candidateHealthStateLockedV4(key)
	if h.State == healthClosed {
		return true, false, generation, ""
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = c.ID
	}
	if h.ProbeInFlight {
		return false, false, generation, fmt.Sprintf("候选 %s 健康状态=%s，已有恢复探测正在执行，跳过", name, h.State)
	}
	if h.State == healthOpen && !h.NextProbeAt.IsZero() && now.Before(h.NextProbeAt) {
		retryMs := h.NextProbeAt.Sub(now).Milliseconds()
		if retryMs < 0 {
			retryMs = 0
		}
		return false, false, generation, fmt.Sprintf("候选 %s 健康状态=OPEN，约 %dms 后允许恢复探测，跳过", name, retryMs)
	}
	h.State = healthHalfOpen
	h.ProbeInFlight = true
	return true, true, generation, ""
}

func acquireCandidateHealthV4(p *Policy, r *PolicyRule, c *PolicyCandidate, now time.Time) (bool, bool) {
	ok, probe, _, _ := acquireCandidateHealthWithReasonV4(p, r, c, now)
	return ok, probe
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

func chooseHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int, acquire bool, healthSkips *[]string) (*PolicyCandidate, bool) {
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
				ok, probe, generation, skipReason := acquireCandidateHealthWithReasonV4(p, r, c, now)
				if ok {
					// Return a per-attempt clone so concurrent requests never race while
					// carrying the generation captured atomically with health acquisition.
					acquired := *c
					acquired.runtimeGeneration = generation
					return &acquired, probe
				}
				if healthSkips != nil && skipReason != "" {
					*healthSkips = append(*healthSkips, skipReason)
				}
			} else if candidateWouldBeSelectableV4(p, r, c, now) {
				return c, false
			}
		}
	}
	return nil, false
}

func nextHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) (*PolicyCandidate, bool) {
	return chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, true, nil)
}

func nextHealthyCandidateWithSkipsV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) (*PolicyCandidate, bool, []string) {
	skips := []string{}
	c, probe := chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, true, &skips)
	return c, probe, skips
}

func peekHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) *PolicyCandidate {
	c, _ := chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, false, nil)
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
	if !candidateHealthConfigCurrentLockedV4(p, r, c) {
		return
	}
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
		// Preserve a stronger deadline that may have been extended by a stale
		// 401/403/429 response while this probe was in flight.
		h.BackoffLevel = level
		h.State = healthOpen
		h.ProbeInFlight = false
		h.OpenedAt = now
		if h.NextProbeAt.IsZero() || proposedDeadline.After(h.NextProbeAt) {
			h.NextProbeAt = proposedDeadline
		}
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

func recordCandidateSuccessV4(p *Policy, r *PolicyRule, c *PolicyCandidate, probeOwned bool) {
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return
	}
	now := time.Now()
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	if !candidateHealthConfigCurrentLockedV4(p, r, c) {
		return
	}
	h := candidateHealthStateLockedV4(key)
	h.LastSuccessAt = now

	wasClosed := h.State == "" || h.State == healthClosed
	ownsCurrentProbe := probeOwned && h.State == healthHalfOpen && h.ProbeInFlight
	if !wasClosed && !ownsCurrentProbe {
		// A stale normal success is telemetry only; it cannot cancel an OPEN
		// recovery cycle or another request's HALF_OPEN probe lease.
		return
	}

	h.State = healthClosed
	h.ProbeInFlight = false
	h.ConsecutiveFailures = 0
	h.BackoffLevel = 0
	h.LastStatus = 0
	h.LastError = ""
	h.OpenedAt = time.Time{}
	h.NextProbeAt = time.Time{}
}

func clearProbeOwnershipV4(owned *bool) {
	if owned != nil {
		*owned = false
	}
}

func releaseCandidateProbeV4(p *Policy, r *PolicyRule, c *PolicyCandidate, owned bool) {
	if !owned {
		return
	}
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return
	}
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	if !candidateHealthConfigCurrentLockedV4(p, r, c) {
		return
	}
	h := v4Runtime.health[key]
	if h == nil || !h.ProbeInFlight {
		return
	}
	h.ProbeInFlight = false
	h.State = healthOpen
	now := time.Now()
	if h.NextProbeAt.IsZero() || h.NextProbeAt.Before(now) {
		h.NextProbeAt = now
	}
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

func candidateHealthConfigEqualV4(a, b *PolicyCandidate) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.TrimSpace(a.ResourceID) == strings.TrimSpace(b.ResourceID) &&
		strings.TrimSpace(a.ResourceKind) == strings.TrimSpace(b.ResourceKind) &&
		strings.TrimSpace(a.Provider) == strings.TrimSpace(b.Provider) &&
		strings.TrimSpace(a.AuthIndex) == strings.TrimSpace(b.AuthIndex) &&
		strings.TrimSpace(a.OverrideModel) == strings.TrimSpace(b.OverrideModel) &&
		a.Enabled == b.Enabled
}

func candidateHealthConfigCurrentLockedV4(p *Policy, r *PolicyRule, c *PolicyCandidate) bool {
	if p == nil || r == nil || c == nil {
		return false
	}
	if c.runtimeGeneration != 0 && c.runtimeGeneration != v4Runtime.generation {
		return false
	}
	activePolicy := v4Runtime.state.Policies[strings.TrimSpace(p.KeyFingerprint)]
	if activePolicy == nil {
		return false
	}
	for _, activeRule := range activePolicy.Rules {
		if activeRule == nil || activeRule.ID != r.ID {
			continue
		}
		for _, activeCandidate := range activeRule.Candidates {
			if activeCandidate != nil && activeCandidate.ID == c.ID {
				return candidateHealthConfigEqualV4(activeCandidate, c)
			}
		}
		return false
	}
	return false
}

func resetChangedCandidateHealthLockedV4(oldPolicy, newPolicy *Policy) {
	if oldPolicy == nil || newPolicy == nil || len(v4Runtime.health) == 0 {
		return
	}
	oldRules := make(map[string]*PolicyRule, len(oldPolicy.Rules))
	for _, r := range oldPolicy.Rules {
		if r != nil {
			oldRules[r.ID] = r
		}
	}
	for _, newRule := range newPolicy.Rules {
		if newRule == nil {
			continue
		}
		oldRule := oldRules[newRule.ID]
		if oldRule == nil {
			continue
		}
		oldCandidates := make(map[string]*PolicyCandidate, len(oldRule.Candidates))
		for _, c := range oldRule.Candidates {
			if c != nil {
				oldCandidates[c.ID] = c
			}
		}
		for _, newCandidate := range newRule.Candidates {
			if newCandidate == nil {
				continue
			}
			oldCandidate := oldCandidates[newCandidate.ID]
			if oldCandidate == nil || candidateHealthConfigEqualV4(oldCandidate, newCandidate) {
				continue
			}
			delete(v4Runtime.health, candidateHealthKeyV4(oldPolicy, oldRule, oldCandidate))
		}
	}
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
