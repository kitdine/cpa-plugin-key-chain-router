package main

import (
	"database/sql"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type routeStatRowV83 struct {
	TraceID   string
	At        string
	Decision  string
	Reason    string
	Policy    string
	Model     string
	Provider  string
	Status    int
	Success   bool
	Error     string
	Attempts  []attemptResult
}

type routeCounterV83 struct {
	Name     string `json:"name"`
	Direct   int    `json:"direct,omitempty"`
	Fallback int    `json:"fallback,omitempty"`
	Failed   int    `json:"failed,omitempty"`
	Count    int    `json:"count,omitempty"`
	Share    float64 `json:"share,omitempty"`
}

type routeTrendPointV83 struct {
	At       string `json:"at"`
	Direct   int    `json:"direct"`
	Fallback int    `json:"fallback"`
	Failed   int    `json:"failed"`
}

func queryRouteStatsV83(q url.Values) map[string]any {
	if rows, ok := routeStatRowsSQLiteV83(q); ok {
		return aggregateRouteStatsV83(rows, q, "sqlite")
	}
	return aggregateRouteStatsV83(routeStatRowsMemoryV83(q), q, "memory")
}

func routeStatRowsMemoryV83(q url.Values) []routeStatRowV83 {
	v4Runtime.RLock()
	recent := append([]RoutingEvent(nil), v4Runtime.recent...)
	v4Runtime.RUnlock()

	policy := strings.TrimSpace(q.Get("policy"))
	provider := strings.TrimSpace(q.Get("provider"))
	model := strings.TrimSpace(q.Get("model"))
	cutoff := eventCutoffV6(strings.TrimSpace(q.Get("since")))

	out := make([]routeStatRowV83, 0, len(recent))
	for _, ev := range recent {
		if ev.Reason == "no_policy" {
			continue
		}
		if !eventMatchesV6(ev, "", "", "", policy, "", provider, model, "", cutoff) {
			continue
		}
		out = append(out, routeStatRowV83{
			TraceID: ev.TraceID, At: ev.At, Decision: ev.Decision, Reason: ev.Reason,
			Policy: ev.PolicyName, Model: ev.Model, Provider: ev.Provider, Status: ev.Status,
			Success: ev.Success, Error: ev.Error, Attempts: append([]attemptResult(nil), ev.Attempts...),
		})
	}
	return out
}

func routeStatRowsSQLiteV83(q url.Values) ([]routeStatRowV83, bool) {
	v4Runtime.RLock()
	obs := normalizeObservability(v4Runtime.state.Observability)
	sink := v4Runtime.sqlite
	v4Runtime.RUnlock()
	if !obs.SQLiteEnabled || sink == nil || sink.db == nil || !sqliteWriterHealthyV62() {
		return nil, false
	}

	where, args := routeStatsSQLiteWhereV83(q)
	rows, err := sink.db.Query(`SELECT
		COALESCE(routing_events.trace_id,''), COALESCE(routing_events.at,''), COALESCE(routing_events.decision,''),
		COALESCE(routing_events.reason,''), COALESCE(routing_events.policy_name,''), COALESCE(routing_events.model,''),
		COALESCE(routing_events.provider,''), COALESCE(routing_events.status,0), COALESCE(routing_events.success,0),
		COALESCE(routing_events.error,''), COALESCE(ra.sequence,0), COALESCE(ra.candidate,''),
		COALESCE(ra.provider,''), COALESCE(ra.auth_index,''), COALESCE(ra.model,''), COALESCE(ra.status,0),
		COALESCE(ra.error,''), COALESCE(ra.duration_ms,0)
		FROM routing_events
		LEFT JOIN routing_attempts ra ON ra.trace_id=routing_events.trace_id`+where+
		` ORDER BY routing_events.at ASC, routing_events.trace_id ASC, ra.sequence ASC`, args...)
	if err != nil {
		recordSQLiteOpenErrorV62(err)
		return nil, false
	}
	defer rows.Close()

	out := []routeStatRowV83{}
	index := map[string]int{}
	for rows.Next() {
		var r routeStatRowV83
		var success, sequence int
		var a attemptResult
		if err := rows.Scan(&r.TraceID, &r.At, &r.Decision, &r.Reason, &r.Policy, &r.Model,
			&r.Provider, &r.Status, &success, &r.Error, &sequence, &a.Candidate,
			&a.Provider, &a.AuthIndex, &a.Model, &a.Status, &a.Error, &a.DurationMs); err != nil {
			recordSQLiteOpenErrorV62(err)
			return nil, false
		}
		r.Success = success != 0
		pos, ok := index[r.TraceID]
		if !ok {
			pos = len(out)
			index[r.TraceID] = pos
			out = append(out, r)
		}
		if sequence > 0 {
			out[pos].Attempts = append(out[pos].Attempts, a)
		}
	}
	if err := rows.Err(); err != nil {
		recordSQLiteOpenErrorV62(err)
		return nil, false
	}
	return out, true
}

func routeStatsSQLiteWhereV83(q url.Values) (string, []any) {
	clauses := []string{`COALESCE(routing_events.reason,'') <> 'no_policy'`}
	args := []any{}
	if cutoff := eventCutoffV6(strings.TrimSpace(q.Get("since"))); !cutoff.IsZero() {
		clauses = append(clauses, `routing_events.at >= ?`)
		args = append(args, cutoff.Format(time.RFC3339Nano))
	}
	if v := strings.TrimSpace(q.Get("policy")); v != "" && v != "all" {
		clauses = append(clauses, `LOWER(COALESCE(routing_events.policy_name,'')) = LOWER(?)`)
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.Get("model")); v != "" && v != "all" {
		clauses = append(clauses, `LOWER(COALESCE(routing_events.model,'')) = LOWER(?)`)
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.Get("provider")); v != "" && v != "all" {
		clauses = append(clauses, `(LOWER(COALESCE(routing_events.provider,'')) = LOWER(?) OR EXISTS (SELECT 1 FROM routing_attempts fa WHERE fa.trace_id=routing_events.trace_id AND LOWER(COALESCE(fa.provider,'')) = LOWER(?)))`)
		args = append(args, v, v)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func aggregateRouteStatsV83(rows []routeStatRowV83, q url.Values, source string) map[string]any {
	total, direct, fallback, failed := 0, 0, 0, 0
	candidate := map[string]*routeCounterV83{}
	paths := map[string]int{}
	reasons := map[string]int{}
	attempts := map[string]int{}
	policyFallbacks := map[string]int{}
	eligible := make([]routeStatRowV83, 0, len(rows))

	for _, r := range rows {
		if r.Decision != decisionHandled && r.Decision != decisionFallbackToCPA {
			continue
		}
		eligible = append(eligible, r)
		total++
		n := len(r.Attempts)
		isFallback := r.Success && (n > 1 || r.Decision == decisionFallbackToCPA)
		switch {
		case !r.Success:
			failed++
		case isFallback:
			fallback++
			if strings.TrimSpace(r.Policy) != "" {
				policyFallbacks[r.Policy]++
			}
		default:
			direct++
		}

		bucket := strconv.Itoa(n)
		if n >= 4 {
			bucket = "4+"
		}
		attempts[bucket]++

		if n > 0 {
			name := strings.TrimSpace(r.Attempts[n-1].Candidate)
			if name == "" {
				name = "未知候选"
			}
			c := candidate[name]
			if c == nil {
				c = &routeCounterV83{Name: name}
				candidate[name] = c
			}
			if !r.Success {
				c.Failed++
			} else if isFallback {
				c.Fallback++
			} else {
				c.Direct++
			}
		}

		if n > 1 || r.Decision == decisionFallbackToCPA {
			path := routePathV83(r.Attempts)
			if path != "" {
				paths[path]++
			}
		}
		if !r.Success {
			reasons[routeFailureReasonV83(r)]++
		}
	}

	candidates := make([]routeCounterV83, 0, len(candidate))
	for _, c := range candidate {
		c.Count = c.Direct + c.Fallback + c.Failed
		candidates = append(candidates, *c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Count != candidates[j].Count {
			return candidates[i].Count > candidates[j].Count
		}
		return candidates[i].Name < candidates[j].Name
	})
	if len(candidates) > 12 {
		candidates = candidates[:12]
	}

	pathTotal := 0
	for _, count := range paths {
		pathTotal += count
	}
	fallbackPaths := countersFromMapV83(paths, pathTotal)
	if len(fallbackPaths) > 10 {
		fallbackPaths = fallbackPaths[:10]
	}
	failureReasons := countersFromMapV83(reasons, failed)

	attemptDist := []routeCounterV83{}
	for _, k := range []string{"0", "1", "2", "3", "4+"} {
		if attempts[k] == 0 {
			continue
		}
		share := float64(0)
		if total > 0 {
			share = float64(attempts[k]) * 100 / float64(total)
		}
		attemptDist = append(attemptDist, routeCounterV83{Name: k, Count: attempts[k], Share: share})
	}

	return map[string]any{
		"ok": true,
		"source": source,
		"stats": map[string]any{
			"total": total,
			"direct": direct,
			"fallback": fallback,
			"failed": failed,
		},
		"candidate_hits": candidates,
		"fallback_paths": fallbackPaths,
		"failure_reasons": failureReasons,
		"attempts": attemptDist,
		"policy_fallbacks": policyFallbacks,
		"trend": routeTrendV83(eligible, q.Get("since")),
	}
}

func routePathV83(xs []attemptResult) string {
	parts := make([]string, 0, len(xs))
	for _, a := range xs {
		name := strings.TrimSpace(a.Candidate)
		if name == "" {
			name = "未知候选"
		}
		if len(parts) == 0 || parts[len(parts)-1] != name {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, " → ")
}

func routeFailureReasonV83(r routeStatRowV83) string {
	status := r.Status
	errText := strings.ToLower(strings.TrimSpace(r.Error))
	if len(r.Attempts) > 0 {
		last := r.Attempts[len(r.Attempts)-1]
		if last.Status != 0 {
			status = last.Status
		}
		if strings.TrimSpace(last.Error) != "" {
			errText = strings.ToLower(last.Error)
		}
	}
	switch {
	case status == 429:
		return "429 Too Many Requests"
	case status >= 500 && status < 600:
		return "5xx Server Error"
	case status == 408 || strings.Contains(errText, "timeout") || strings.Contains(errText, "deadline"):
		return "超时"
	case status == 401 || status == 403:
		return "401 / 403"
	case status == 409:
		return "409 Conflict"
	case status == 0 && errText != "":
		return "网络错误"
	case strings.Contains(r.Reason, "affinity") || strings.Contains(r.Reason, "candidate"):
		return "路由配置"
	default:
		return "其他"
	}
}

func countersFromMapV83(in map[string]int, total int) []routeCounterV83 {
	out := make([]routeCounterV83, 0, len(in))
	for name, count := range in {
		share := float64(0)
		if total > 0 {
			share = float64(count) * 100 / float64(total)
		}
		out = append(out, routeCounterV83{Name: name, Count: count, Share: share})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func routeTrendV83(rows []routeStatRowV83, since string) []routeTrendPointV83 {
	if len(rows) == 0 {
		return []routeTrendPointV83{}
	}
	now := time.Now().UTC()
	start := eventCutoffV6(strings.TrimSpace(since))
	if start.IsZero() {
		start = now
		for _, r := range rows {
			if at, err := time.Parse(time.RFC3339Nano, r.At); err == nil && at.Before(start) {
				start = at
			}
		}
	}
	if !start.Before(now) {
		start = now.Add(-time.Hour)
	}
	const buckets = 12
	span := now.Sub(start)
	step := span / buckets
	if step < time.Minute {
		step = time.Minute
		start = now.Add(-step * buckets)
	}
	out := make([]routeTrendPointV83, buckets)
	for i := 0; i < buckets; i++ {
		out[i].At = start.Add(time.Duration(i) * step).Format(time.RFC3339Nano)
	}
	for _, r := range rows {
		at, err := time.Parse(time.RFC3339Nano, r.At)
		if err != nil || at.Before(start) || at.After(now) {
			continue
		}
		idx := int(at.Sub(start) / step)
		if idx >= buckets {
			idx = buckets - 1
		}
		if idx < 0 {
			continue
		}
		n := len(r.Attempts)
		switch {
		case !r.Success:
			out[idx].Failed++
		case n > 1 || r.Decision == decisionFallbackToCPA:
			out[idx].Fallback++
		default:
			out[idx].Direct++
		}
	}
	return out
}

// keep database/sql linked here so older sqlite build tags remain explicit in this file.
var _ = sql.ErrNoRows
