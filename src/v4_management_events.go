package main

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func queryEventsV4(q url.Values) map[string]any {
	if out, ok := queryEventsSQLiteV62(q); ok {
		return out
	}
	v4Runtime.RLock()
	recent := append([]RoutingEvent(nil), v4Runtime.recent...)
	obs := normalizeObservability(v4Runtime.state.Observability)
	v4Runtime.RUnlock()

	limit := parseEventLimitV6(q.Get("limit"))
	offset := parseEventOffsetV83(q.Get("offset"))
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
	matchedIndex := 0
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

		if matchedIndex >= offset && len(events) < limit {
			events = append(events, ev)
		}
		matchedIndex++
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
		"events":        events,
		"sqlite_status": sqliteStatusV62(),
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

func parseEventOffsetV83(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	if n > 10000000 {
		return 10000000
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
