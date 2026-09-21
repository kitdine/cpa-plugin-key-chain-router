package main

import "testing"

func TestResourceAliasUsesStableAuthIndexIdentity(t *testing.T) {
	r1 := apiResource{ID: "runtime-a", Kind: "API", Provider: "claude", AuthIndex: "idx-1", DisplayName: "Claude API A"}
	r2 := apiResource{ID: "runtime-b", Kind: "API", Provider: "claude", AuthIndex: "idx-1", DisplayName: "Claude API B"}
	if got, want := resourceAliasKeyV82(r1), resourceAliasKeyV82(r2); got != want {
		t.Fatalf("alias key changed with runtime resource id: %q != %q", got, want)
	}
}

func TestApplyResourceAliasesV82(t *testing.T) {
	r := apiResource{ID: "r1", Kind: "OAuth", Provider: "codex", AuthIndex: "oauth-1", DisplayName: "user@example.com"}
	key := resourceAliasKeyV82(r)
	got := applyResourceAliasesV82([]apiResource{r}, map[string]string{key: "Codex Pro"})
	if len(got) != 1 || got[0].Alias != "Codex Pro" {
		t.Fatalf("alias not applied: %#v", got)
	}
	if got[0].DisplayName != "user@example.com" {
		t.Fatalf("source display name must remain unchanged: %#v", got[0])
	}
}

func TestDownstreamAliasUsesPolicyName(t *testing.T) {
	keys := []downstreamKey{{Fingerprint: "fp", Hint: "sk-…123"}}
	policies := map[string]*Policy{"fp": {Name: "claude-mem", KeyFingerprint: "fp"}}
	got := applyDownstreamAliasesV82(keys, policies)
	if len(got) != 1 || got[0].Alias != "claude-mem" {
		t.Fatalf("downstream alias = %#v", got)
	}
}

func TestStickyHeaderRequiredForHeaderSource(t *testing.T) {
	p := &Policy{
		Name: "sticky",
		KeyFingerprint: "fp",
		ClientAffinity: clientAffinityOff,
		Rules: []*PolicyRule{{
			Name: "sticky-rule",
			Models: []string{"*"},
			Strategy: strategySticky,
			StickySource: "header",
			StickyHeader: "",
			Candidates: []*PolicyCandidate{{
				ID: "c1", Name: "c1", Provider: "claude", ResourceKind: "API", Enabled: true, Priority: 100, Weight: 1,
			}},
			Failover: defaultFailover(),
		}},
	}
	if err := validatePolicy(p); err == nil {
		t.Fatal("expected missing sticky header validation error")
	}
	p.Rules[0].StickyHeader = "X-Session-Id"
	if err := validatePolicy(p); err != nil {
		t.Fatalf("valid sticky header rejected: %v", err)
	}
}
