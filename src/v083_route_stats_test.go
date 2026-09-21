package main

import (
	"net/url"
	"testing"
)

func TestAggregateRouteStatsV83(t *testing.T) {
	rows := []routeStatRowV83{
		{
			TraceID: "direct", At: nowV4(), Decision: decisionHandled, Policy: "p1", Success: true,
			Attempts: []attemptResult{{Candidate: "A", Provider: "codex", Status: 200}},
		},
		{
			TraceID: "fallback", At: nowV4(), Decision: decisionHandled, Policy: "p1", Success: true,
			Attempts: []attemptResult{{Candidate: "A", Provider: "codex", Status: 429}, {Candidate: "B", Provider: "codex", Status: 200}},
		},
		{
			TraceID: "failed", At: nowV4(), Decision: decisionHandled, Policy: "p2", Success: false, Status: 500,
			Attempts: []attemptResult{{Candidate: "C", Provider: "claude", Status: 500}},
		},
		{
			TraceID: "bypass", At: nowV4(), Decision: decisionBypass, Policy: "p2", Success: true,
		},
	}
	out := aggregateRouteStatsV83(rows, url.Values{"since":{"1h"}}, "memory")
	stats := out["stats"].(map[string]any)
	if stats["total"].(int) != 3 || stats["direct"].(int) != 1 || stats["fallback"].(int) != 1 || stats["failed"].(int) != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	paths := out["fallback_paths"].([]routeCounterV83)
	if len(paths) != 1 || paths[0].Name != "A → B" || paths[0].Count != 1 {
		t.Fatalf("fallback paths = %#v", paths)
	}
	reasons := out["failure_reasons"].([]routeCounterV83)
	if len(reasons) != 1 || reasons[0].Name != "5xx Server Error" {
		t.Fatalf("failure reasons = %#v", reasons)
	}
	pf := out["policy_fallbacks"].(map[string]int)
	if pf["p1"] != 1 {
		t.Fatalf("policy fallbacks = %#v", pf)
	}
}

func TestQueryEventsV83PaginationAndRouteOnly(t *testing.T) {
	recent := []RoutingEvent{
		{TraceID:"a", At:nowV4(), Decision:decisionHandled, PolicyName:"p", Success:true, Attempts:[]attemptResult{{Candidate:"A",Status:200}}},
		{TraceID:"bypass", At:nowV4(), Decision:decisionBypass, PolicyName:"p", Success:true},
		{TraceID:"b", At:nowV4(), Decision:decisionHandled, PolicyName:"p", Success:true, Attempts:[]attemptResult{{Candidate:"B",Status:200}}},
		{TraceID:"c", At:nowV4(), Decision:decisionHandled, PolicyName:"p", Success:true, Attempts:[]attemptResult{{Candidate:"C",Status:200}}},
	}
	withV6Runtime(t, V4State{Policies:map[string]*Policy{}, Observability:ObservabilityConfig{MemoryEnabled:true,MemoryLimit:500}}, recent)

	out := queryEventsV4(url.Values{"route_only":{"true"},"limit":{"1"},"offset":{"1"}})
	if out["matched"].(int) != 3 || out["returned"].(int) != 1 {
		t.Fatalf("counts = %#v", out)
	}
	events := out["events"].([]RoutingEvent)
	// queryEventsV4 walks newest to oldest, so route-only order is c,b,a and offset 1 => b.
	if len(events) != 1 || events[0].TraceID != "b" {
		t.Fatalf("events = %#v", events)
	}
}

func TestRouteFailureReasonV83(t *testing.T) {
	cases := []struct{
		row routeStatRowV83
		want string
	}{
		{routeStatRowV83{Status:429}, "429 Too Many Requests"},
		{routeStatRowV83{Status:503}, "5xx Server Error"},
		{routeStatRowV83{Status:408}, "超时"},
		{routeStatRowV83{Status:0, Error:"dial tcp failed"}, "网络错误"},
	}
	for _, tc := range cases {
		if got := routeFailureReasonV83(tc.row); got != tc.want {
			t.Fatalf("reason=%q want=%q", got, tc.want)
		}
	}
}
