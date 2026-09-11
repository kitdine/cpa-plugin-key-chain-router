from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {n}")
    p.write_text(text.replace(old, new, 1))


# Runtime health map.
replace_once(
    "src/v4_types.go",
    """\tstate  V4State
\trecent []RoutingEvent
\trr     map[string]uint64
\tsmooth map[string]map[string]int
\tsqlite *sqliteSink
}{
\trr:     map[string]uint64{},
\tsmooth: map[string]map[string]int{},
}
""",
    """\tstate  V4State
\trecent []RoutingEvent
\trr     map[string]uint64
\tsmooth map[string]map[string]int
\thealth map[string]*candidateHealthState
\tsqlite *sqliteSink
}{
\trr:     map[string]uint64{},
\tsmooth: map[string]map[string]int{},
\thealth: map[string]*candidateHealthState{},
}
""",
)

replace_once(
    "src/v4_state.go",
    """\tv4Runtime.recent = nil
\tv4Runtime.rr = map[string]uint64{}
\tv4Runtime.smooth = map[string]map[string]int{}
\tv4Runtime.Unlock()
""",
    """\tv4Runtime.recent = nil
\tv4Runtime.rr = map[string]uint64{}
\tv4Runtime.smooth = map[string]map[string]int{}
\tv4Runtime.health = map[string]*candidateHealthState{}
\tv4Runtime.Unlock()
""",
)

# Management visibility and stale-state pruning on policy edits.
replace_once(
    "src/v4_management.go",
    """\t\tv4Runtime.state.Policies[p.KeyFingerprint] = clonePolicyV4(&p)
\t\tv4Runtime.Unlock()
""",
    """\t\tv4Runtime.state.Policies[p.KeyFingerprint] = clonePolicyV4(&p)
\t\tpruneCandidateHealthLockedV4()
\t\tv4Runtime.Unlock()
""",
)
replace_once(
    "src/v4_management.go",
    """\t\tdelete(v4Runtime.state.Policies, fp)
\t\tv4Runtime.Unlock()
""",
    """\t\tdelete(v4Runtime.state.Policies, fp)
\t\tpruneCandidateHealthLockedV4()
\t\tv4Runtime.Unlock()
""",
)
replace_once(
    "src/v4_management.go",
    """\t\t\"observability\":   st.Observability,
\t\t\"sqlite_status\":   sqliteStatusV62(),
\t\t\"recent_events\":   recent,
""",
    """\t\t\"observability\":   st.Observability,
\t\t\"sqlite_status\":   sqliteStatusV62(),
\t\t\"health_defaults\": candidateHealthDefaultsV4(),
\t\t\"candidate_health\": candidateHealthSnapshotV4(),
\t\t\"recent_events\":   recent,
""",
)
replace_once(
    "src/v4_management.go",
    """\tfor i, c := range ranked {
\t\titems = append(items, map[string]any{\"order\": i + 1, \"name\": c.Name, \"provider\": c.Provider, \"auth_index\": c.AuthIndex, \"priority\": c.Priority, \"weight\": c.Weight, \"override_model\": c.OverrideModel})
\t}
""",
    """\tfor i, c := range ranked {
\t\titems = append(items, map[string]any{\"order\": i + 1, \"name\": c.Name, \"provider\": c.Provider, \"auth_index\": c.AuthIndex, \"priority\": c.Priority, \"weight\": c.Weight, \"override_model\": c.OverrideModel, \"health\": candidateHealthViewV4(p, rule, c)})
\t}
""",
)

# Non-stream selection becomes health aware; record outcome; lookahead must not acquire probe lease.
replace_once(
    "src/v4_execution.go",
    """\t\tc := nextCandidateV4(ranked, attempted, failNext, currentPriority)
\t\tif c == nil {
""",
    """\t\tc, _ := nextHealthyCandidateV4(p, r, ranked, attempted, failNext, currentPriority)
\t\tif c == nil {
""",
)
replace_once(
    "src/v4_execution.go",
    """\t\tevent.Attempts = append(event.Attempts, ar)
\t\tif err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400 {
\t\t\tevent.Final = c.Name
""",
    """\t\tevent.Attempts = append(event.Attempts, ar)
\t\tif err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400 {
\t\t\trecordCandidateSuccessV4(p, r, c)
\t\t\tevent.Final = c.Name
""",
)
replace_once(
    "src/v4_execution.go",
    """\t\tif err != nil {
\t\t\tlastErr = err
\t\t} else {
\t\t\tlastErr = fmt.Errorf(\"upstream status %d\", resp.StatusCode)
\t\t}
\t\taction := failureActionV4(r.Failover, ar.Status, err)
""",
    """\t\tif err != nil {
\t\t\tlastErr = err
\t\t} else {
\t\t\tlastErr = fmt.Errorf(\"upstream status %d\", resp.StatusCode)
\t\t}
\t\trecordCandidateFailureV4(p, r, c, ar.Status, err, resp.Headers)
\t\taction := failureActionV4(r.Failover, ar.Status, err)
""",
)
replace_once(
    "src/v4_execution.go",
    """\t\tnext := nextCandidateV4(ranked, attempted, action, currentPriority)
\t\tif next == nil {
""",
    """\t\tnext := peekHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority)
\t\tif next == nil {
""",
)

# Streaming path: health-aware selection.
# There is exactly one remaining initial candidate selection in runStreamPolicyV4.
replace_once(
    "src/v4_execution.go",
    """\t\tc := nextCandidateV4(ranked, attempted, failNext, currentPriority)
\t\tif c == nil {
""",
    """\t\tc, _ := nextHealthyCandidateV4(p, r, ranked, attempted, failNext, currentPriority)
\t\tif c == nil {
""",
)

# callHost error: mark failure and use non-acquiring lookahead.
replace_once(
    "src/v4_execution.go",
    """\t\tif err != nil {
\t\t\tar.Error = err.Error()
\t\t\tar.Status = statusFromError(err)
\t\t\tevent.Attempts = append(event.Attempts, ar)
\t\t\tlastErr = err
\t\t\taction := failureActionV4(r.Failover, ar.Status, err)
""",
    """\t\tif err != nil {
\t\t\tar.Error = err.Error()
\t\t\tar.Status = statusFromError(err)
\t\t\tevent.Attempts = append(event.Attempts, ar)
\t\t\tlastErr = err
\t\t\trecordCandidateFailureV4(p, r, c, ar.Status, err, nil)
\t\t\taction := failureActionV4(r.Failover, ar.Status, err)
""",
)
# Replace the streaming lookahead (the non-stream one was already replaced above).
replace_once(
    "src/v4_execution.go",
    """\t\t\tnext := nextCandidateV4(ranked, attempted, action, currentPriority)
\t\t\tif next == nil {
""",
    """\t\t\tnext := peekHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority)
\t\t\tif next == nil {
""",
)

# Stream response decode failures count as transient candidate failures and release half-open lease.
replace_once(
    "src/v4_execution.go",
    """\t\tvar sr hostModelStreamResponse
\t\tif err = json.Unmarshal(raw, &sr); err != nil {
\t\t\tar.Error = err.Error()
\t\t\tevent.Attempts = append(event.Attempts, ar)
\t\t\tlastErr = err
\t\t\tcontinue
\t\t}
\t\tar.Status = sr.StatusCode
\t\tevent.Attempts = append(event.Attempts, ar)
\t\tif sr.StreamID == \"\" {
\t\t\tlastErr = errors.New(\"host stream id 为空\")
\t\t\tcontinue
\t\t}
""",
    """\t\tvar sr hostModelStreamResponse
\t\tif err = json.Unmarshal(raw, &sr); err != nil {
\t\t\tar.Error = err.Error()
\t\t\tevent.Attempts = append(event.Attempts, ar)
\t\t\tlastErr = err
\t\t\trecordCandidateFailureV4(p, r, c, 0, err, nil)
\t\t\tcontinue
\t\t}
\t\tar.Status = sr.StatusCode
\t\tevent.Attempts = append(event.Attempts, ar)
\t\tif sr.StatusCode < 200 || sr.StatusCode >= 400 {
\t\t\tif sr.StreamID != \"\" {
\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t}
\t\t\tlastErr = fmt.Errorf(\"upstream status %d\", sr.StatusCode)
\t\t\tevent.Attempts[len(event.Attempts)-1].Error = lastErr.Error()
\t\t\trecordCandidateFailureV4(p, r, c, sr.StatusCode, nil, sr.Headers)
\t\t\taction := failureActionV4(r.Failover, sr.StatusCode, nil)
\t\t\tif action == failCPADefault {
\t\t\t\trunCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
\t\t\t\treturn
\t\t\t}
\t\t\tif action == failStop {
\t\t\t\tbreak
\t\t\t}
\t\t\tnext := peekHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority)
\t\t\tif next == nil {
\t\t\t\tbreak
\t\t\t}
\t\t\tranked = moveCandidateFirstV4(ranked, next.ID)
\t\t\tcontinue
\t\t}
\t\tif sr.StreamID == \"\" {
\t\t\tlastErr = errors.New(\"host stream id 为空\")
\t\t\tevent.Attempts[len(event.Attempts)-1].Error = lastErr.Error()
\t\t\trecordCandidateFailureV4(p, r, c, 0, lastErr, sr.Headers)
\t\t\tcontinue
\t\t}
""",
)

# Read errors before first chunk make the candidate unhealthy; after first chunk they also affect future health.
replace_once(
    "src/v4_execution.go",
    """\t\t\tif e != nil {
\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\tlastErr = e
\t\t\t\tif first {
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tevent.Success = false
""",
    """\t\t\tif e != nil {
\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\tlastErr = e
\t\t\t\trecordCandidateFailureV4(p, r, c, statusFromError(e), e, sr.Headers)
\t\t\t\tif first {
\t\t\t\t\tevent.Attempts[len(event.Attempts)-1].Error = e.Error()
\t\t\t\t\tevent.Attempts[len(event.Attempts)-1].Status = statusFromError(e)
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tevent.Success = false
""",
)
replace_once(
    "src/v4_execution.go",
    """\t\t\tif rr.Error != \"\" {
\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\tlastErr = errors.New(rr.Error)
\t\t\t\tif first {
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tevent.Success = false
""",
    """\t\t\tif rr.Error != \"\" {
\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\tlastErr = errors.New(rr.Error)
\t\t\t\trecordCandidateFailureV4(p, r, c, 0, lastErr, sr.Headers)
\t\t\t\tif first {
\t\t\t\t\tevent.Attempts[len(event.Attempts)-1].Error = rr.Error
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tevent.Success = false
""",
)

# Successful completed stream closes circuit. Downstream emit failure must not leave half-open lease stuck.
replace_once(
    "src/v4_execution.go",
    """\t\t\t\tif e = emitOutput(outStreamID, rr.Payload); e != nil {
\t\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\t\treturn
\t\t\t\t}
""",
    """\t\t\t\tif e = emitOutput(outStreamID, rr.Payload); e != nil {
\t\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\t\treleaseCandidateProbeV4(p, r, c)
\t\t\t\t\t_ = closeOutputStream(outStreamID, e.Error())
\t\t\t\t\treturn
\t\t\t\t}
""",
)
replace_once(
    "src/v4_execution.go",
    """\t\t\tif rr.Done {
\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\tevent.Final = c.Name
""",
    """\t\t\tif rr.Done {
\t\t\t\t_ = closeHostStream(sr.StreamID)
\t\t\t\trecordCandidateSuccessV4(p, r, c)
\t\t\t\tevent.Final = c.Name
""",
)

# New health implementation.
Path("src/v4_health.go").write_text(r'''package main

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

func recordCandidateFailureV4(p *Policy, r *PolicyRule, c *PolicyCandidate, status int, err error, headers http.Header) {
    key := candidateHealthKeyV4(p, r, c)
    if key == "" {
        return
    }
    now := time.Now()
    v4Runtime.Lock()
    defer v4Runtime.Unlock()
    h := candidateHealthStateLockedV4(key)
    cooldown, qualifies := candidateFailureCooldownV4(status, err, headers, h.BackoffLevel, now)
    if !qualifies {
        // A half-open probe that reaches the upstream and receives a request-specific
        // non-health status proves transport reachability; do not strand the lease.
        if h.ProbeInFlight || h.State == healthHalfOpen {
            h.State = healthClosed
            h.ProbeInFlight = false
            h.ConsecutiveFailures = 0
            h.BackoffLevel = 0
            h.NextProbeAt = time.Time{}
            h.OpenedAt = time.Time{}
        }
        return
    }
    h.State = healthOpen
    h.ProbeInFlight = false
    h.ConsecutiveFailures++
    h.LastStatus = status
    h.LastError = ""
    if err != nil {
        h.LastError = err.Error()
    }
    h.LastFailureAt = now
    h.OpenedAt = now
    h.NextProbeAt = now.Add(cooldown)
    if h.BackoffLevel < 30 {
        h.BackoffLevel++
    }
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
    if !h.LastFailureAt.IsZero() { out.LastFailureAt = h.LastFailureAt.UTC().Format(time.RFC3339Nano) }
    if !h.LastSuccessAt.IsZero() { out.LastSuccessAt = h.LastSuccessAt.UTC().Format(time.RFC3339Nano) }
    if !h.OpenedAt.IsZero() { out.OpenedAt = h.OpenedAt.UTC().Format(time.RFC3339Nano) }
    if !h.NextProbeAt.IsZero() {
        out.NextProbeAt = h.NextProbeAt.UTC().Format(time.RFC3339Nano)
        if h.NextProbeAt.After(now) { out.RetryInMs = h.NextProbeAt.Sub(now).Milliseconds() }
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
        if out[i].PolicyName != out[j].PolicyName { return out[i].PolicyName < out[j].PolicyName }
        if out[i].RuleName != out[j].RuleName { return out[i].RuleName < out[j].RuleName }
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
        if p == nil { continue }
        for _, r := range p.Rules {
            if r == nil { continue }
            for _, c := range r.Candidates {
                if c == nil { continue }
                if key := candidateHealthKeyV4(p, r, c); key != "" { valid[key] = struct{}{} }
            }
        }
    }
    for key := range v4Runtime.health {
        if _, ok := valid[key]; !ok { delete(v4Runtime.health, key) }
    }
}
''')

Path("src/issue13_health_test.go").write_text(r'''package main

import (
    "errors"
    "net/http"
    "testing"
    "time"
)

func issue13Fixture() (*Policy, *PolicyRule, []*PolicyCandidate) {
    a := &PolicyCandidate{ID: "a", Name: "A", Provider: "codex", AuthIndex: "idx-a", Enabled: true, Priority: 100, Weight: 1}
    b := &PolicyCandidate{ID: "b", Name: "B", Provider: "codex", AuthIndex: "idx-b", Enabled: true, Priority: 90, Weight: 1}
    r := &PolicyRule{ID: "r1", Name: "rule", Strategy: strategyOrdered, Candidates: []*PolicyCandidate{a, b}, Failover: defaultFailover()}
    p := &Policy{Name: "policy", KeyFingerprint: "fp", Enabled: true, Rules: []*PolicyRule{r}}
    return p, r, []*PolicyCandidate{a, b}
}

func resetIssue13Health(t *testing.T) {
    t.Helper()
    v4Runtime.Lock()
    old := v4Runtime.health
    v4Runtime.health = map[string]*candidateHealthState{}
    v4Runtime.Unlock()
    t.Cleanup(func() {
        v4Runtime.Lock()
        v4Runtime.health = old
        v4Runtime.Unlock()
    })
}

func TestIssue13FailureSkipsCandidateUntilSingleHalfOpenProbe(t *testing.T) {
    resetIssue13Health(t)
    p, r, ranked := issue13Fixture()
    a := ranked[0]

    recordCandidateFailureV4(p, r, a, 503, nil, nil)
    attempted := map[string]bool{}
    got, probe := nextHealthyCandidateV4(p, r, ranked, attempted, failNext, int(^uint(0)>>1))
    if got == nil || got.ID != "b" || probe {
        t.Fatalf("during cooldown got=(%v, probe=%v), want B non-probe", got, probe)
    }

    key := candidateHealthKeyV4(p, r, a)
    v4Runtime.Lock()
    v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
    v4Runtime.Unlock()

    got, probe = nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
    if got == nil || got.ID != "a" || !probe {
        t.Fatalf("after cooldown got=(%v, probe=%v), want A half-open probe", got, probe)
    }

    got, probe = nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
    if got == nil || got.ID != "b" || probe {
        t.Fatalf("concurrent probe got=(%v, probe=%v), want B while A probe is in flight", got, probe)
    }
}

func TestIssue13ProbeSuccessFailsBackToPreferredCandidate(t *testing.T) {
    resetIssue13Health(t)
    p, r, ranked := issue13Fixture()
    a := ranked[0]
    recordCandidateFailureV4(p, r, a, 503, nil, nil)
    key := candidateHealthKeyV4(p, r, a)
    v4Runtime.Lock()
    v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
    v4Runtime.Unlock()
    got, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
    if got == nil || got.ID != "a" || !probe { t.Fatalf("expected A probe, got=%v probe=%v", got, probe) }

    recordCandidateSuccessV4(p, r, a)
    got, probe = nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
    if got == nil || got.ID != "a" || probe {
        t.Fatalf("after successful probe got=(%v, probe=%v), want normal A", got, probe)
    }
    view := candidateHealthViewV4(p, r, a)
    if view.State != healthClosed || view.BackoffLevel != 0 || view.ConsecutiveFailures != 0 {
        t.Fatalf("health after recovery = %#v", view)
    }
}

func TestIssue13ProbeFailureUsesExponentialBackoff(t *testing.T) {
    resetIssue13Health(t)
    p, r, ranked := issue13Fixture()
    a := ranked[0]
    recordCandidateFailureV4(p, r, a, 503, nil, nil)
    key := candidateHealthKeyV4(p, r, a)
    v4Runtime.RLock()
    first := v4Runtime.health[key].NextProbeAt.Sub(v4Runtime.health[key].LastFailureAt)
    v4Runtime.RUnlock()
    if first != 30*time.Second { t.Fatalf("first cooldown=%v, want 30s", first) }

    v4Runtime.Lock()
    v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
    v4Runtime.Unlock()
    if got, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1)); got == nil || got.ID != "a" || !probe {
        t.Fatalf("expected half-open A probe, got=%v probe=%v", got, probe)
    }
    recordCandidateFailureV4(p, r, a, 503, nil, nil)
    v4Runtime.RLock()
    second := v4Runtime.health[key].NextProbeAt.Sub(v4Runtime.health[key].LastFailureAt)
    v4Runtime.RUnlock()
    if second != 60*time.Second { t.Fatalf("second cooldown=%v, want 60s", second) }
}

func TestIssue13RateLimitHonorsRetryAfter(t *testing.T) {
    resetIssue13Health(t)
    p, r, ranked := issue13Fixture()
    a := ranked[0]
    h := http.Header{"Retry-After": []string{"120"}}
    recordCandidateFailureV4(p, r, a, 429, nil, h)
    key := candidateHealthKeyV4(p, r, a)
    v4Runtime.RLock()
    cooldown := v4Runtime.health[key].NextProbeAt.Sub(v4Runtime.health[key].LastFailureAt)
    v4Runtime.RUnlock()
    if cooldown < 120*time.Second { t.Fatalf("429 cooldown=%v, want >=120s", cooldown) }
}

func TestIssue13NonHealthFailureDoesNotOpenClosedCandidate(t *testing.T) {
    resetIssue13Health(t)
    p, r, ranked := issue13Fixture()
    a := ranked[0]
    recordCandidateFailureV4(p, r, a, 400, errors.New("bad request"), nil)
    view := candidateHealthViewV4(p, r, a)
    if view.State != healthClosed { t.Fatalf("400 must not open circuit: %#v", view) }
}
''')
