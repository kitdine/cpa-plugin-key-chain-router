package main

import (
	"net/http"
	"testing"
)

func testCandidate(id string, priority, weight int) *PolicyCandidate {
	return &PolicyCandidate{ID: id, Name: id, Provider: "codex", AuthIndex: "idx-" + id, Enabled: true, Priority: priority, Weight: weight}
}

func TestV4RuleMatchSpecificity(t *testing.T) {
	exact := &PolicyRule{Models: []string{"gpt-5.6-luna"}}
	glob := &PolicyRule{Models: []string{"gpt-5.6-*"}}
	all := &PolicyRule{Models: []string{"*"}}
	if !(ruleMatchScoreV4(exact, "gpt-5.6-luna") > ruleMatchScoreV4(glob, "gpt-5.6-luna")) {
		t.Fatal("exact match must outrank glob")
	}
	if !(ruleMatchScoreV4(glob, "gpt-5.6-luna") > ruleMatchScoreV4(all, "gpt-5.6-luna")) {
		t.Fatal("glob must outrank catch-all")
	}
}

func TestV4PolicyRejectsDuplicateExactModelsAndCatchAll(t *testing.T) {
	p := &Policy{KeyFingerprint: "fp", Rules: []*PolicyRule{
		{Name: "a", Models: []string{"gpt-x"}, Strategy: strategyCPADefault},
		{Name: "b", Models: []string{"gpt-x"}, Strategy: strategyCPADefault},
	}}
	if err := validatePolicy(p); err == nil {
		t.Fatal("expected duplicate exact model validation error")
	}
	p.Rules = []*PolicyRule{
		{Name: "a", Models: []string{"*"}, Strategy: strategyCPADefault},
		{Name: "b", Models: []string{"*"}, Strategy: strategyCPADefault},
	}
	if err := validatePolicy(p); err == nil {
		t.Fatal("expected duplicate catch-all validation error")
	}
}

func TestV4OneLegacyKeyMigratesToOnePolicy(t *testing.T) {
	legacy := State{Routes: map[string]*Route{
		"r1": {ID: "r1", Name: "luna", KeyFingerprint: "fp", KeyHint: "sk-a…1", Enabled: true, MatchModels: []string{"luna"}, Candidates: []*Candidate{{ID: "a", Name: "A", Enabled: true}}},
		"r2": {ID: "r2", Name: "sol", KeyFingerprint: "fp", KeyHint: "sk-a…1", Enabled: true, MatchModels: []string{"sol"}, Candidates: []*Candidate{{ID: "b", Name: "B", Enabled: true}}},
	}}
	got := migrateLegacyV4(legacy)
	if len(got.Policies) != 1 {
		t.Fatalf("policies = %d, want 1", len(got.Policies))
	}
	if len(got.Policies["fp"].Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(got.Policies["fp"].Rules))
	}
}

func TestV4RoundRobinRotates(t *testing.T) {
	v4Runtime.Lock()
	v4Runtime.rr = map[string]uint64{}
	v4Runtime.Unlock()
	p := &Policy{KeyFingerprint: "fp"}
	r := &PolicyRule{ID: "r", Strategy: strategyRoundRobin, Candidates: []*PolicyCandidate{testCandidate("a", 100, 1), testCandidate("b", 100, 1), testCandidate("c", 100, 1)}}
	seen := []string{}
	for i := 0; i < 4; i++ {
		seen = append(seen, rankCandidatesV4(p, r, http.Header{}, nil)[0].ID)
	}
	want := []string{"a", "b", "c", "a"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("sequence %v, want %v", seen, want)
		}
	}
}

func TestV4SmoothWeightedRoundRobinDistribution(t *testing.T) {
	v4Runtime.Lock()
	v4Runtime.smooth = map[string]map[string]int{}
	v4Runtime.Unlock()
	p := &Policy{KeyFingerprint: "fp"}
	r := &PolicyRule{ID: "w", Strategy: strategyWeightedRR, Candidates: []*PolicyCandidate{testCandidate("a", 100, 5), testCandidate("b", 100, 3), testCandidate("c", 100, 2)}}
	counts := map[string]int{}
	for i := 0; i < 100; i++ {
		counts[rankCandidatesV4(p, r, http.Header{}, nil)[0].ID]++
	}
	if counts["a"] != 50 || counts["b"] != 30 || counts["c"] != 20 {
		t.Fatalf("weighted counts = %#v", counts)
	}
}

func TestV4PriorityWeightedNeverStartsLowerPriority(t *testing.T) {
	v4Runtime.Lock()
	v4Runtime.smooth = map[string]map[string]int{}
	v4Runtime.Unlock()
	p := &Policy{KeyFingerprint: "fp"}
	r := &PolicyRule{ID: "p", Strategy: strategyPriorityWeighted, Candidates: []*PolicyCandidate{testCandidate("top-a", 100, 1), testCandidate("top-b", 100, 1), testCandidate("low", 50, 100)}}
	for i := 0; i < 20; i++ {
		if got := rankCandidatesV4(p, r, http.Header{}, nil)[0].Priority; got != 100 {
			t.Fatalf("first priority = %d, want 100", got)
		}
	}
}

func TestV4StickyIsStable(t *testing.T) {
	p := &Policy{KeyFingerprint: "fp"}
	r := &PolicyRule{ID: "s", Strategy: strategySticky, StickySource: "header", StickyHeader: "X-Session-Id", Candidates: []*PolicyCandidate{testCandidate("a", 100, 5), testCandidate("b", 100, 3), testCandidate("c", 100, 2)}}
	h := http.Header{"X-Session-Id": []string{"session-123"}}
	first := rankCandidatesV4(p, r, h, nil)[0].ID
	for i := 0; i < 20; i++ {
		if got := rankCandidatesV4(p, r, h, nil)[0].ID; got != first {
			t.Fatalf("sticky candidate changed: %s -> %s", first, got)
		}
	}
}

func TestV4FailoverActions(t *testing.T) {
	f := defaultFailover()
	f.RateLimit = failSamePriorityFirst
	f.Unauthorized = failNextPriority
	if got := failureActionV4(f, 429, nil); got != failSamePriorityFirst {
		t.Fatalf("429 action = %q", got)
	}
	if got := failureActionV4(f, 401, nil); got != failNextPriority {
		t.Fatalf("401 action = %q", got)
	}
}

func TestV4ObservabilityDefaultsSafe(t *testing.T) {
	o := defaultObservability()
	if !o.MemoryEnabled || !o.LogEnabled || o.SQLiteEnabled || o.ResponseHeaders {
		t.Fatalf("unsafe defaults: %#v", o)
	}
}
