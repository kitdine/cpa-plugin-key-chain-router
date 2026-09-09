package main

import (
	"encoding/json"
	"hash/fnv"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func handleModelRouteV4(raw []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil { return nil, err }
	runtimeState.RLock(); cfg := runtimeState.cfg; runtimeState.RUnlock()
	if !cfg.Enabled { return okEnvelope(map[string]any{"Handled": false}) }
	headers := mapToHeader(anyMap(req["Headers"])); if len(headers) == 0 { headers = mapToHeader(anyMap(req["headers"])) }
	key := extractDownstreamKey(headers); if key == "" { return okEnvelope(map[string]any{"Handled": false}) }
	fp := fingerprint(key)
	model := stringAny(req, "RequestedModel"); if model == "" { model = stringAny(req, "requested_model") }
	callbackID := stringAny(req, "host_callback_id")
	p, rule := findPolicyRuleV4(fp, model)
	if p == nil {
		observeV4(RoutingEvent{TraceID: traceIDV4(), At: nowV4(), Decision: decisionBypass, Reason: "no_policy", KeyFingerprint: fp, KeyHint: maskKey(key), Model: model, Success: true}, callbackID)
		return okEnvelope(map[string]any{"Handled": false, "Reason": "kcr_no_policy"})
	}
	if rule == nil {
		observeV4(RoutingEvent{TraceID: traceIDV4(), At: nowV4(), Decision: decisionBypass, Reason: "no_matching_rule", PolicyName: p.Name, KeyFingerprint: fp, KeyHint: p.KeyHint, Model: model, Success: true}, callbackID)
		return okEnvelope(map[string]any{"Handled": false, "Reason": "kcr_no_matching_rule"})
	}
	if rule.Strategy == strategyCPADefault {
		observeV4(RoutingEvent{TraceID: traceIDV4(), At: nowV4(), Decision: decisionBypass, Reason: "rule_cpa_default", PolicyName: p.Name, KeyFingerprint: fp, KeyHint: p.KeyHint, RuleID: rule.ID, RuleName: rule.Name, Strategy: rule.Strategy, Model: model, Success: true}, callbackID)
		return okEnvelope(map[string]any{"Handled": false, "Reason": "kcr_rule_cpa_default"})
	}
	if len(enabledV4Candidates(rule)) == 0 {
		observeV4(RoutingEvent{TraceID: traceIDV4(), At: nowV4(), Decision: decisionBypass, Reason: "no_enabled_candidates", PolicyName: p.Name, KeyFingerprint: fp, KeyHint: p.KeyHint, RuleID: rule.ID, RuleName: rule.Name, Strategy: rule.Strategy, Model: model, Success: false}, callbackID)
		return okEnvelope(map[string]any{"Handled": false, "Reason": "kcr_no_enabled_candidates"})
	}
	return okEnvelope(map[string]any{"Handled": true, "TargetKind": "self", "Target": pluginID, "Reason": "kcr_policy:" + p.KeyFingerprint + ":" + rule.ID})
}

func handleExecuteV4(raw []byte, streaming bool) ([]byte, error) {
	var req map[string]any; if err := json.Unmarshal(raw, &req); err != nil { return nil, err }
	callbackID := stringAny(req, "host_callback_id"); streamID := stringAny(req, "stream_id")
	model := stringAny(req, "Model"); if model == "" { model = stringAny(req, "model") }
	source := stringAny(req, "SourceFormat"); if source == "" { source = stringAny(req, "source_format") }; if source == "" { source = "openai" }
	original := bytesAny(req, "OriginalRequest"); if len(original) == 0 { original = bytesAny(req, "original_request") }
	payload := bytesAny(req, "Payload"); if len(payload) == 0 { payload = bytesAny(req, "payload") }
	body := original; if len(body) == 0 { body = payload }
	headers := mapToHeader(anyMap(req["Headers"])); if len(headers) == 0 { headers = mapToHeader(anyMap(req["headers"])) }
	query := mapToValues(anyMap(req["Query"])); if len(query) == 0 { query = mapToValues(anyMap(req["query"])) }
	metadata := anyMap(req["Metadata"]); if metadata == nil { metadata = anyMap(req["metadata"]) }
	alt := stringAny(req, "Alt"); if alt == "" { alt = stringAny(req, "alt") }
	key := extractDownstreamKey(headers); if key == "" { key = stringAny(metadata, "api_key") }; if key == "" { return errorEnvelope("policy_key_missing", "无法识别 CPA 原生 API Key"), nil }
	fp := fingerprint(key)
	if len(original) > 0 { var m map[string]any; if json.Unmarshal(original, &m) == nil { if x := stringAny(m, "model"); x != "" { model = x } } }
	p, rule := findPolicyRuleV4(fp, model); if p == nil || rule == nil || rule.Strategy == strategyCPADefault { return errorEnvelope("policy_not_found", "KCR executor 未找到已匹配 Policy/Rule"), nil }
	trace := traceIDV4(); started := time.Now(); ranked := rankCandidatesV4(p, rule, headers, metadata)
	if streaming {
		go runStreamPolicyV4(trace, p, rule, ranked, source, model, body, headers, query, alt, callbackID, streamID, started)
		outHeaders := http.Header{"Content-Type": []string{"text/event-stream"}}
		if obsV4().ResponseHeaders { outHeaders.Set("X-KCR-Decision", decisionHandled); outHeaders.Set("X-KCR-Trace-ID", trace) }
		return okEnvelope(map[string]any{"headers": outHeaders})
	}
	resp, event, err := runNonStreamPolicyV4(trace, p, rule, ranked, source, model, body, headers, query, alt, callbackID, started); observeV4(event, callbackID)
	if err != nil { return errorEnvelope("executor_error", err.Error()), nil }
	if obsV4().ResponseHeaders { if resp.Headers == nil { resp.Headers = http.Header{} }; resp.Headers.Set("X-KCR-Decision", event.Decision); resp.Headers.Set("X-KCR-Trace-ID", trace); resp.Headers.Set("X-KCR-Rule", rule.Name) }
	return okEnvelope(map[string]any{"Payload": resp.Body, "Headers": resp.Headers})
}

func findPolicyRuleV4(fp, model string) (*Policy, *PolicyRule) {
	v4Runtime.RLock(); p := clonePolicyV4(v4Runtime.state.Policies[fp]); v4Runtime.RUnlock(); if p == nil || !p.Enabled { return nil, nil }
	var best *PolicyRule; bestScore := -1
	for i, r := range p.Rules { if r == nil { continue }; score := ruleMatchScoreV4(r, model); if score < 0 { continue }; score = score*1000 + (len(p.Rules)-i); if score > bestScore { bestScore = score; best = r } }
	return p, cloneRuleV4(best)
}

func ruleMatchScoreV4(r *PolicyRule, model string) int {
	best := -1
	for _, p := range r.Models {
		p = strings.TrimSpace(p); if p == "" { continue }
		if p == "*" { if best < 0 { best = 0 }; continue }
		if !strings.ContainsAny(p, "*?") { if strings.EqualFold(p, model) { s := 100000 + len(p); if s > best { best = s } }; continue }
		if wildcardMatch(p, model) { literal := len(strings.ReplaceAll(strings.ReplaceAll(p, "*", ""), "?", "")); s := 1000 + literal; if s > best { best = s } }
	}
	return best
}

func enabledV4Candidates(r *PolicyRule) []*PolicyCandidate {
	out := []*PolicyCandidate{}; if r == nil { return out }
	for _, c := range r.Candidates { if c != nil && c.Enabled { cc := *c; if cc.Priority == 0 { cc.Priority = 100 }; if cc.Weight <= 0 { cc.Weight = 1 }; out = append(out, &cc) } }
	return out
}

func rankCandidatesV4(p *Policy, r *PolicyRule, headers http.Header, meta map[string]any) []*PolicyCandidate {
	cs := enabledV4Candidates(r); if len(cs) < 2 { return cs }; key := p.KeyFingerprint + ":" + r.ID
	switch r.Strategy {
	case strategyRoundRobin:
		v4Runtime.Lock(); n := v4Runtime.rr[key]; v4Runtime.rr[key] = n + 1; v4Runtime.Unlock(); start := int(n % uint64(len(cs))); return append(append([]*PolicyCandidate{}, cs[start:]...), cs[:start]...)
	case strategyWeightedRR:
		first := smoothPickV4(key, cs); return weightedRemainderV4(first, cs)
	case strategyPriorityWeighted:
		sort.SliceStable(cs, func(i, j int) bool { if cs[i].Priority == cs[j].Priority { return cs[i].Weight > cs[j].Weight }; return cs[i].Priority > cs[j].Priority }); top := cs[0].Priority; group := []*PolicyCandidate{}; for _, c := range cs { if c.Priority == top { group = append(group, c) } }; first := smoothPickV4(key+":"+strconv.Itoa(top), group); return priorityRemainderV4(first, cs)
	case strategySticky:
		sticky := stickyKeyV4(r, headers, meta); if sticky == "" { sticky = p.KeyFingerprint }; sort.SliceStable(cs, func(i, j int) bool { if cs[i].Priority != cs[j].Priority { return cs[i].Priority > cs[j].Priority }; return rendezvousScoreV4(sticky, cs[i]) > rendezvousScoreV4(sticky, cs[j]) }); return cs
	default:
		return cs
	}
}

func smoothPickV4(key string, cs []*PolicyCandidate) *PolicyCandidate {
	if len(cs) == 0 { return nil }
	v4Runtime.Lock(); defer v4Runtime.Unlock(); cur := v4Runtime.smooth[key]; if cur == nil { cur = map[string]int{}; v4Runtime.smooth[key] = cur }
	total := 0; var best *PolicyCandidate; bestVal := math.MinInt
	for _, c := range cs { w := c.Weight; if w <= 0 { w = 1 }; total += w; cur[c.ID] += w; if cur[c.ID] > bestVal { bestVal = cur[c.ID]; best = c } }
	if best != nil { cur[best.ID] -= total }; return best
}

func weightedRemainderV4(first *PolicyCandidate, cs []*PolicyCandidate) []*PolicyCandidate {
	out := []*PolicyCandidate{}; if first != nil { out = append(out, first) }; rest := []*PolicyCandidate{}; for _, c := range cs { if first == nil || c.ID != first.ID { rest = append(rest, c) } }; sort.SliceStable(rest, func(i, j int) bool { return rest[i].Weight > rest[j].Weight }); return append(out, rest...)
}

func priorityRemainderV4(first *PolicyCandidate, cs []*PolicyCandidate) []*PolicyCandidate {
	out := []*PolicyCandidate{}; if first != nil { out = append(out, first) }; rest := []*PolicyCandidate{}; for _, c := range cs { if first == nil || c.ID != first.ID { rest = append(rest, c) } }; sort.SliceStable(rest, func(i, j int) bool { if rest[i].Priority == rest[j].Priority { return rest[i].Weight > rest[j].Weight }; return rest[i].Priority > rest[j].Priority }); return append(out, rest...)
}

func stickyKeyV4(r *PolicyRule, h http.Header, m map[string]any) string {
	source := strings.ToLower(strings.TrimSpace(r.StickySource)); if source == "" { source = "auto" }
	if source == "header" && r.StickyHeader != "" { return h.Get(r.StickyHeader) }
	if source == "session" || source == "auto" { for _, k := range []string{"session_id", "sessionId", "trace_id"} { if s := stringAny(m, k); s != "" { return s } }; for _, k := range []string{"X-Claude-Code-Session-Id", "X-Session-Id", "Session-Id", "X-Request-Id"} { if s := h.Get(k); s != "" { return s } } }
	if r.StickyHeader != "" { return h.Get(r.StickyHeader) }; return ""
}

func rendezvousScoreV4(key string, c *PolicyCandidate) float64 {
	h := fnv.New64a(); _, _ = h.Write([]byte(key)); _, _ = h.Write([]byte{0}); _, _ = h.Write([]byte(c.ID)); x := h.Sum64(); u := (float64(x)+1)/(float64(math.MaxUint64)+1); w := c.Weight; if w <= 0 { w = 1 }; return math.Pow(u, 1/float64(w))
}
