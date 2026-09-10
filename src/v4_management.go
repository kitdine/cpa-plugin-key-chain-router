package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func handleAPIV4(req managementRequest) (map[string]any, error) {
	action := strings.TrimSpace(req.Query.Get("action"))
	if action == "" {
		action = "snapshot"
	}
	switch action {
	case "snapshot":
		return buildSnapshotV4(), nil
	case "events":
		return queryEventsV4(req.Query), nil
	case "save_policy":
		payload := payloadV4(req)
		if len(payload) == 0 {
			return nil, errors.New("payload is required")
		}
		var p Policy
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid policy payload: %w", err)
		}
		normalizePolicyV4(&p)
		if err := validatePolicy(&p); err != nil {
			return nil, err
		}
		v4Runtime.Lock()
		if v4Runtime.state.Policies == nil {
			v4Runtime.state.Policies = map[string]*Policy{}
		}
		v4Runtime.state.Policies[p.KeyFingerprint] = clonePolicyV4(&p)
		v4Runtime.Unlock()
		if err := saveV4State(); err != nil {
			return nil, err
		}
		return buildSnapshotV4(), nil
	case "delete_policy":
		fp := strings.TrimSpace(req.Query.Get("fingerprint"))
		v4Runtime.Lock()
		delete(v4Runtime.state.Policies, fp)
		v4Runtime.Unlock()
		if err := saveV4State(); err != nil {
			return nil, err
		}
		return buildSnapshotV4(), nil
	case "diagnose":
		return diagnosePolicyV4(strings.TrimSpace(req.Query.Get("fingerprint")), strings.TrimSpace(req.Query.Get("model"))), nil
	case "save_observability":
		payload := payloadV4(req)
		if len(payload) == 0 {
			return nil, errors.New("payload is required")
		}
		var o ObservabilityConfig
		if err := json.Unmarshal(payload, &o); err != nil {
			return nil, err
		}
		o = normalizeObservability(o)
		v4Runtime.Lock()
		v4Runtime.state.Observability = o
		v4Runtime.Unlock()
		if err := saveV4State(); err != nil {
			return nil, err
		}
		if err := restartSQLiteSinkV4(); err != nil {
			return nil, err
		}
		return buildSnapshotV4(), nil
	case "clear_memory":
		v4Runtime.Lock()
		v4Runtime.recent = nil
		v4Runtime.Unlock()
		return buildSnapshotV4(), nil
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

func payloadV4(req managementRequest) []byte {
	if len(req.Body) > 0 {
		return append([]byte(nil), req.Body...)
	}
	if x := req.Query.Get("payload"); x != "" {
		return []byte(x)
	}
	return nil
}

func normalizePolicyV4(p *Policy) {
	if p == nil {
		return
	}
	p.KeyFingerprint = strings.TrimSpace(p.KeyFingerprint)
	if p.Name == "" {
		p.Name = "策略 " + p.KeyHint
	}
	for _, r := range p.Rules {
		normalizeRule(r)
	}
}

func buildSnapshotV4() map[string]any {
	keys, resources, configErr := currentEnvironment()
	v4Runtime.RLock()
	st := cloneV4State(v4Runtime.state)
	recent := append([]RoutingEvent(nil), v4Runtime.recent...)
	v4Runtime.RUnlock()

	rebindPoliciesV4(&st, resources)
	policies := make([]*Policy, 0, len(st.Policies))
	for _, p := range st.Policies {
		policies = append(policies, p)
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })

	recent = visibleRecentEventsV6(recent)
	if len(recent) > 200 {
		recent = recent[len(recent)-200:]
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}

	runtimeState.RLock()
	schema := runtimeState.schema
	cfgPath := runtimeState.configPath
	runtimeState.RUnlock()
	return map[string]any{
		"ok":              true,
		"version":         pluginVersion,
		"schema":          schema,
		"config_path":     cfgPath,
		"config_error":    configErr,
		"downstream_keys": keys,
		"resources":       resources,
		"policies":        policies,
		"observability":   st.Observability,
		"recent_events":   recent,
	}
}

func visibleRecentEventsV6(events []RoutingEvent) []RoutingEvent {
	out := make([]RoutingEvent, 0, len(events))
	for _, ev := range events {
		if ev.Reason == "no_policy" {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func queryEventsV4(q url.Values) map[string]any {
	v4Runtime.RLock()
	recent := append([]RoutingEvent(nil), v4Runtime.recent...)
	obs := normalizeObservability(v4Runtime.state.Observability)
	v4Runtime.RUnlock()

	limit := parseEventLimitV6(q.Get("limit"))
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	decision := strings.TrimSpace(q.Get("decision"))
	success := strings.TrimSpace(q.Get("success"))
	policy := strings.TrimSpace(q.Get("policy"))
	strategy := strings.TrimSpace(q.Get("strategy"))
	provider := strings.TrimSpace(q.Get("provider"))
	model := strings.TrimSpace(q.Get("model"))
	statusBucket := strings.TrimSpace(q.Get("status"))
	cutoff := eventCutoffV6(strings.TrimSpace(q.Get("since")))

	events := make([]RoutingEvent, 0, minV6(limit, len(recent)))
	durations := make([]int64, 0, len(recent))
	totalAttempts := 0
	windowTotal := 0
	stats := map[string]any{
		"total":    0,
		"handled":  0,
		"fallback": 0,
		"bypass":   0,
		"success":  0,
		"failed":   0,
	}

	facetPolicies := map[string]struct{}{}
	facetStrategies := map[string]struct{}{}
	facetProviders := map[string]struct{}{}
	facetModels := map[string]struct{}{}

	for i := len(recent) - 1; i >= 0; i-- {
		ev := recent[i]
		if ev.Reason == "no_policy" {
			continue
		}
		windowTotal++
		addFacetV6(facetPolicies, ev.PolicyName)
		addFacetV6(facetStrategies, ev.Strategy)
		addFacetV6(facetProviders, ev.Provider)
		addFacetV6(facetModels, ev.Model)
		for _, a := range ev.Attempts {
			addFacetV6(facetProviders, a.Provider)
		}

		if !eventMatchesV6(ev, search, decision, success, policy, strategy, provider, model, statusBucket, cutoff) {
			continue
		}

		stats["total"] = stats["total"].(int) + 1
		switch ev.Decision {
		case decisionHandled:
			stats["handled"] = stats["handled"].(int) + 1
		case decisionFallbackToCPA:
			stats["fallback"] = stats["fallback"].(int) + 1
		case decisionBypass:
			stats["bypass"] = stats["bypass"].(int) + 1
		}
		if ev.Success {
			stats["success"] = stats["success"].(int) + 1
		} else {
			stats["failed"] = stats["failed"].(int) + 1
		}
		if ev.DurationMs >= 0 {
			durations = append(durations, ev.DurationMs)
		}
		totalAttempts += len(ev.Attempts)

		if len(events) < limit {
			events = append(events, ev)
		}
	}

	total := stats["total"].(int)
	if total > 0 {
		stats["success_rate"] = float64(stats["success"].(int)) * 100 / float64(total)
		stats["avg_attempts"] = float64(totalAttempts) / float64(total)
	} else {
		stats["success_rate"] = float64(0)
		stats["avg_attempts"] = float64(0)
	}
	if len(durations) > 0 {
		var sum int64
		for _, d := range durations {
			sum += d
		}
		stats["avg_duration_ms"] = sum / int64(len(durations))
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		idx := (len(durations)*95 + 99) / 100
		if idx < 1 {
			idx = 1
		}
		stats["p95_duration_ms"] = durations[idx-1]
	} else {
		stats["avg_duration_ms"] = int64(0)
		stats["p95_duration_ms"] = int64(0)
	}

	return map[string]any{
		"ok":           true,
		"source":       "memory",
		"memory_limit": obs.MemoryLimit,
		"memory_on":    obs.MemoryEnabled,
		"window_total": windowTotal,
		"matched":      total,
		"returned":     len(events),
		"stats":        stats,
		"facets": map[string]any{
			"policies":   sortedFacetV6(facetPolicies),
			"strategies": sortedFacetV6(facetStrategies),
			"providers":  sortedFacetV6(facetProviders),
			"models":     sortedFacetV6(facetModels),
		},
		"events": events,
	}
}

func parseEventLimitV6(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 200
	}
	if n > 1000 {
		return 1000
	}
	return n
}

func eventCutoffV6(raw string) time.Time {
	if raw == "" || raw == "all" {
		return time.Time{}
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return time.Time{}
	}
	return time.Now().UTC().Add(-d)
}

func eventMatchesV6(ev RoutingEvent, search, decision, success, policy, strategy, provider, model, statusBucket string, cutoff time.Time) bool {
	if !cutoff.IsZero() {
		at, err := time.Parse(time.RFC3339Nano, ev.At)
		if err == nil && at.Before(cutoff) {
			return false
		}
	}
	if decision != "" && decision != "all" && ev.Decision != decision {
		return false
	}
	if success == "true" && !ev.Success {
		return false
	}
	if success == "false" && ev.Success {
		return false
	}
	if policy != "" && policy != "all" && !strings.EqualFold(ev.PolicyName, policy) {
		return false
	}
	if strategy != "" && strategy != "all" && !strings.EqualFold(ev.Strategy, strategy) {
		return false
	}
	if provider != "" && provider != "all" && !eventHasProviderV6(ev, provider) {
		return false
	}
	if model != "" && model != "all" && !strings.EqualFold(ev.Model, model) {
		return false
	}
	if statusBucket != "" && statusBucket != "all" && !eventStatusMatchesV6(ev, statusBucket) {
		return false
	}
	if search != "" && !strings.Contains(strings.ToLower(eventSearchTextV6(ev)), search) {
		return false
	}
	return true
}

func eventHasProviderV6(ev RoutingEvent, provider string) bool {
	if strings.EqualFold(ev.Provider, provider) {
		return true
	}
	for _, a := range ev.Attempts {
		if strings.EqualFold(a.Provider, provider) {
			return true
		}
	}
	return false
}

func eventStatusMatchesV6(ev RoutingEvent, bucket string) bool {
	switch bucket {
	case "2xx":
		return ev.Status >= 200 && ev.Status < 300
	case "3xx":
		return ev.Status >= 300 && ev.Status < 400
	case "4xx":
		return ev.Status >= 400 && ev.Status < 500
	case "5xx":
		return ev.Status >= 500 && ev.Status < 600
	case "error":
		return !ev.Success || ev.Status == 0
	default:
		return true
	}
}

func eventSearchTextV6(ev RoutingEvent) string {
	var b strings.Builder
	for _, s := range []string{
		ev.TraceID, ev.Decision, ev.Reason, ev.PolicyName, ev.KeyHint, ev.RuleID,
		ev.RuleName, ev.Strategy, ev.Model, ev.Final, ev.Provider, ev.AuthIndex, ev.Error,
	} {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	for _, s := range ev.SelectionReasons {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	for _, a := range ev.Attempts {
		b.WriteString(a.Candidate)
		b.WriteByte('\n')
		b.WriteString(a.Provider)
		b.WriteByte('\n')
		b.WriteString(a.AuthIndex)
		b.WriteByte('\n')
		b.WriteString(a.Model)
		b.WriteByte('\n')
		b.WriteString(a.Error)
		b.WriteByte('\n')
	}
	return b.String()
}

func addFacetV6(dst map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		dst[value] = struct{}{}
	}
}

func sortedFacetV6(src map[string]struct{}) []string {
	out := make([]string, 0, len(src))
	for v := range src {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func minV6(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func rebindPoliciesV4(st *V4State, resources []apiResource) {
	byID := map[string]apiResource{}
	byIdx := map[string]apiResource{}
	for _, x := range resources {
		byID[x.ID] = x
		if x.AuthIndex != "" {
			byIdx[x.AuthIndex] = x
		}
	}
	for _, p := range st.Policies {
		for _, r := range p.Rules {
			for _, c := range r.Candidates {
				if c == nil {
					continue
				}
				x, ok := byID[c.ResourceID]
				if !ok && c.AuthIndex != "" {
					x, ok = byIdx[c.AuthIndex]
				}
				if ok {
					c.ResourceID = x.ID
					c.ResourceKind = x.Kind
					c.Provider = x.Provider
					if c.Name == "" {
						c.Name = x.DisplayName
					}
				}
			}
		}
	}
}

func diagnosePolicyV4(fp, model string) map[string]any {
	v4Runtime.RLock()
	p := clonePolicyV4(v4Runtime.state.Policies[fp])
	v4Runtime.RUnlock()
	if p == nil {
		return map[string]any{"ok": false, "error": "policy not found"}
	}
	_, resources, _ := currentEnvironment()
	st := V4State{Policies: map[string]*Policy{fp: p}}
	rebindPoliciesV4(&st, resources)
	p = st.Policies[fp]
	_, rule := findPolicyRuleV4(fp, model)
	if rule == nil {
		return map[string]any{"ok": true, "policy": p, "model": model, "matched": false, "decision": decisionBypass, "note": "该模型没有命中任何 Rule，将由 CPA 默认路由处理"}
	}
	ranked := rankCandidatesV4(p, rule, http.Header{}, map[string]any{})
	items := []map[string]any{}
	for i, c := range ranked {
		items = append(items, map[string]any{"order": i + 1, "name": c.Name, "provider": c.Provider, "auth_index": c.AuthIndex, "priority": c.Priority, "weight": c.Weight, "override_model": c.OverrideModel})
	}
	decision := decisionHandled
	if rule.Strategy == strategyCPADefault {
		decision = decisionBypass
	}
	return map[string]any{"ok": true, "policy": p, "model": model, "matched": true, "rule": rule, "strategy": rule.Strategy, "ranked_candidates": items, "decision": decision, "curl": buildDiagnosticCurlV4(p, model)}
}

func buildDiagnosticCurlV4(p *Policy, model string) string {
	if model == "" {
		model = "<MODEL>"
	}
	return fmt.Sprintf("curl -i http://<CPA_HOST>:8317/v1/responses \\\n  -H 'Authorization: Bearer <策略 %s 对应的完整 CPA API Key>' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"%s\",\"input\":\"只回复 ROUTE_OK\"}'", strings.ReplaceAll(p.Name, "'", ""), strings.ReplaceAll(model, "'", ""))
}
