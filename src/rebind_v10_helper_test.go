package main

import "testing"

func TestRebindLegacyUsesProviderScopedIndex(t *testing.T) {
	st := V4State{Policies: map[string]*Policy{"fp": {KeyFingerprint: "fp", Rules: []*PolicyRule{{ID: "r1", Candidates: []*PolicyCandidate{{ID: "c1", Provider: "codex", AuthIndex: "idx", Enabled: true}}}}}}}
	resources := []apiResource{
		{ID: "xai-resource", Kind: "API", Provider: "xai", AuthID: "xai-live", AuthIndex: "idx"},
		{ID: "codex-resource", Kind: "API", Provider: "codex", AuthID: "codex-live", AuthIndex: "idx"},
	}
	rebindPoliciesV4(&st, resources)
	got := st.Policies["fp"].Rules[0].Candidates[0]
	if got.AuthID != "codex-live" || got.Provider != "codex" {
		t.Fatalf("legacy rebind crossed provider: %+v", got)
	}
}
