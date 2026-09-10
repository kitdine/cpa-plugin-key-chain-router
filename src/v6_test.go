package main

import (
	"net/url"
	"strings"
	"testing"
)

func withV6Runtime(t *testing.T, state V4State, recent []RoutingEvent) {
	t.Helper()
	v4Runtime.Lock()
	oldState := cloneV4State(v4Runtime.state)
	oldRecent := append([]RoutingEvent(nil), v4Runtime.recent...)
	v4Runtime.state = cloneV4State(state)
	v4Runtime.recent = append([]RoutingEvent(nil), recent...)
	v4Runtime.Unlock()
	t.Cleanup(func() {
		v4Runtime.Lock()
		v4Runtime.state = oldState
		v4Runtime.recent = oldRecent
		v4Runtime.Unlock()
	})
}

func TestPrepareObservationV6DropsUnconfiguredPolicy(t *testing.T) {
	withV6Runtime(t, V4State{Policies: map[string]*Policy{}}, nil)
	ev := RoutingEvent{Reason: "no_policy", KeyFingerprint: "missing"}
	if prepareObservationV6(&ev) {
		t.Fatal("unconfigured API key should not be observable")
	}
}

func TestPrepareObservationV6KeepsDisabledPolicy(t *testing.T) {
	withV6Runtime(t, V4State{Policies: map[string]*Policy{
		"fp": {Name: "disabled", KeyFingerprint: "fp", KeyHint: "sk-…-x", Enabled: false},
	}}, nil)
	ev := RoutingEvent{Reason: "no_policy", KeyFingerprint: "fp"}
	if !prepareObservationV6(&ev) {
		t.Fatal("configured disabled policy should remain observable")
	}
	if ev.Reason != "policy_disabled" || ev.PolicyName != "disabled" {
		t.Fatalf("event = %#v", ev)
	}
}

func TestEnrichRoutingSelectionReasonsV6(t *testing.T) {
	rule := &PolicyRule{
		ID: "r1", Name: "luna", Models: []string{"gpt-5.6-luna"}, Strategy: strategyPriorityWeighted,
		Candidates: []*PolicyCandidate{
			{ID: "a", Name: "A", Provider: "codex", AuthIndex: "idx-a", Enabled: true, Priority: 100, Weight: 3},
			{ID: "b", Name: "B", Provider: "codex", AuthIndex: "idx-b", Enabled: true, Priority: 50, Weight: 1},
		},
		Failover: FailoverPolicy{RateLimit: failNextPriority, Exhausted: "error"},
	}
	withV6Runtime(t, V4State{Policies: map[string]*Policy{
		"fp": {Name: "p", KeyFingerprint: "fp", Enabled: true, Rules: []*PolicyRule{rule}},
	}}, nil)
	ev := RoutingEvent{
		KeyFingerprint: "fp", RuleID: "r1", RuleName: "luna", Strategy: strategyPriorityWeighted,
		Attempts: []attemptResult{
			{Candidate: "A", Provider: "codex", AuthIndex: "idx-a", Status: 429},
			{Candidate: "B", Provider: "codex", AuthIndex: "idx-b", Status: 200},
		},
	}
	enrichRoutingSelectionReasonsV6(&ev)
	if len(ev.SelectionReasons) != 2 {
		t.Fatalf("selection reasons = %#v", ev.SelectionReasons)
	}
	if !strings.Contains(ev.SelectionReasons[0], "Priority=100") || !strings.Contains(ev.SelectionReasons[0], "Weight=3") {
		t.Fatalf("first reason = %q", ev.SelectionReasons[0])
	}
	if !strings.Contains(ev.SelectionReasons[1], "HTTP 429") || !strings.Contains(ev.SelectionReasons[1], "next-priority") {
		t.Fatalf("second reason = %q", ev.SelectionReasons[1])
	}
}

func TestQueryEventsV4FiltersAndStats(t *testing.T) {
	recent := []RoutingEvent{
		{TraceID: "ignored", At: nowV4(), Decision: decisionBypass, Reason: "no_policy", Model: "claude-sonnet-5", Success: true},
		{TraceID: "ok", At: nowV4(), Decision: decisionHandled, PolicyName: "claude-mem", Strategy: strategyPriorityWeighted, Model: "gpt-5.6-luna", Provider: "codex", Status: 200, DurationMs: 10, Success: true, Attempts: []attemptResult{{Candidate: "A", Provider: "codex", Status: 200}}},
		{TraceID: "fallback", At: nowV4(), Decision: decisionFallbackToCPA, Reason: "policy_fallback_to_cpa", PolicyName: "claude-mem", Strategy: strategyPriorityWeighted, Model: "gpt-5.6-luna", Status: 200, DurationMs: 20, Success: true, Attempts: []attemptResult{{Candidate: "A", Provider: "codex", Status: 429}, {Candidate: "CPA Default", Status: 200}}},
		{TraceID: "fail", At: nowV4(), Decision: decisionHandled, Reason: "candidates_exhausted", PolicyName: "other", Strategy: strategyOrdered, Model: "gpt-5.6-sol", Provider: "codex", Status: 500, DurationMs: 30, Success: false, Attempts: []attemptResult{{Candidate: "B", Provider: "codex", Status: 500}}},
	}
	withV6Runtime(t, V4State{Policies: map[string]*Policy{}, Observability: ObservabilityConfig{MemoryEnabled: true, MemoryLimit: 500}}, recent)

	all := queryEventsV4(url.Values{})
	if all["window_total"].(int) != 3 || all["matched"].(int) != 3 || all["returned"].(int) != 3 {
		t.Fatalf("all counts = %#v", all)
	}
	stats := all["stats"].(map[string]any)
	if stats["success"].(int) != 2 || stats["failed"].(int) != 1 || stats["fallback"].(int) != 1 {
		t.Fatalf("stats = %#v", stats)
	}

	filtered := queryEventsV4(url.Values{"decision": {decisionHandled}, "success": {"true"}, "q": {"luna"}})
	if filtered["matched"].(int) != 1 || filtered["returned"].(int) != 1 {
		t.Fatalf("filtered = %#v", filtered)
	}
	events := filtered["events"].([]RoutingEvent)
	if len(events) != 1 || events[0].TraceID != "ok" {
		t.Fatalf("events = %#v", events)
	}
}
